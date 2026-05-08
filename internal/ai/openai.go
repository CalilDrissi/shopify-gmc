package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// SettingsProvider is the subset of internal/settings.Service that the AI
// client needs. Decoupling via interface keeps tests independent of the
// settings service's full surface.
type SettingsProvider interface {
	Get(ctx context.Context, key string) (string, error)
}

// Setting keys the client reads from the settings service.
const (
	SettingBaseURL = "ai_base_url"
	SettingAPIKey  = "ai_api_key"
	SettingModel   = "ai_model"
)

// Default tuning. Override via NewOpenAIClient options if needed.
const (
	DefaultTimeout      = 60 * time.Second
	DefaultMaxAttempts  = 3
	DefaultBackoffStart = time.Second
	DefaultTemperature  = 0.2
)

// OpenAIClient implements Client against an OpenAI-compatible Chat Completions
// endpoint (works with OpenAI, Anthropic via compat shim, Together, Groq,
// LMStudio, etc.). No SDK; just net/http.
type OpenAIClient struct {
	settings    SettingsProvider
	httpClient  *http.Client
	logger      *slog.Logger
	maxAttempts int
	backoffBase time.Duration
	temperature float64
	now         func() time.Time // for tests / determinism
}

type Option func(*OpenAIClient)

func WithLogger(l *slog.Logger) Option           { return func(c *OpenAIClient) { c.logger = l } }
func WithMaxAttempts(n int) Option               { return func(c *OpenAIClient) { c.maxAttempts = n } }
func WithBackoffBase(d time.Duration) Option     { return func(c *OpenAIClient) { c.backoffBase = d } }
func WithTemperature(t float64) Option           { return func(c *OpenAIClient) { c.temperature = t } }
func WithHTTPClient(h *http.Client) Option       { return func(c *OpenAIClient) { c.httpClient = h } }
func WithClock(fn func() time.Time) Option       { return func(c *OpenAIClient) { c.now = fn } }

func NewOpenAIClient(s SettingsProvider, opts ...Option) *OpenAIClient {
	c := &OpenAIClient{
		settings:    s,
		httpClient:  &http.Client{Timeout: DefaultTimeout},
		logger:      slog.Default(),
		maxAttempts: DefaultMaxAttempts,
		backoffBase: DefaultBackoffStart,
		temperature: DefaultTemperature,
		now:         time.Now,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// ----------------------------------------------------------------------------
// OpenAI wire format
// ----------------------------------------------------------------------------

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
}

type chatChoice struct {
	Index   int         `json:"index"`
	Message chatMessage `json:"message"`
	Finish  string      `json:"finish_reason"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type chatResponse struct {
	ID      string       `json:"id"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
	Error   struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// ----------------------------------------------------------------------------
// Public methods
// ----------------------------------------------------------------------------

func (c *OpenAIClient) GenerateFix(ctx context.Context, req FixRequest) (FixResponse, error) {
	cfg, err := c.config(ctx)
	if err != nil {
		return FixResponse{}, err
	}
	user := fmt.Sprintf(promptFixUser,
		coalesce(req.StoreContext, "(none provided)"),
		req.CheckID, req.Severity, req.Title, req.Detail,
		coalesce(req.ProductTitle, "(no title)"),
		coalesce(req.ProductURL, "(no url)"),
		truncate(req.Evidence, 600),
	)
	body, in, out, err := c.complete(ctx, cfg, promptFixSystem, user)
	if err != nil {
		return FixResponse{}, err
	}
	return FixResponse{
		IssueID:   req.IssueID,
		Suggested: strings.TrimSpace(body),
		TokensIn:  in,
		TokensOut: out,
		ModelUsed: cfg.Model,
	}, nil
}

func (c *OpenAIClient) GenerateFixBatch(ctx context.Context, req BatchFixRequest) ([]FixResponse, error) {
	if len(req.Issues) == 0 {
		return nil, nil
	}
	if len(req.Issues) > 10 {
		return nil, ErrBatchTooLarge
	}
	cfg, err := c.config(ctx)
	if err != nil {
		return nil, err
	}

	var sb strings.Builder
	for i, iss := range req.Issues {
		fmt.Fprintf(&sb, "%d. check=%s severity=%s product=%q url=%s\n   title: %s\n   detail: %s\n",
			i+1, iss.CheckID, iss.Severity,
			coalesce(iss.ProductTitle, "(no title)"),
			coalesce(iss.ProductURL, "(no url)"),
			iss.Title, iss.Detail,
		)
		if iss.Evidence != "" {
			fmt.Fprintf(&sb, "   evidence: %s\n", truncate(iss.Evidence, 240))
		}
	}
	user := fmt.Sprintf(promptFixBatchUser,
		coalesce(req.StoreContext, "(none provided)"),
		sb.String(),
	)
	body, in, out, err := c.complete(ctx, cfg, promptFixBatchSystem, user)
	if err != nil {
		return nil, err
	}
	parsed := parseFixBatch(body, req.Issues)
	for i := range parsed {
		parsed[i].ModelUsed = cfg.Model
		// Attribute prompt tokens evenly across issues for log/metric purposes.
		parsed[i].TokensIn = in / len(parsed)
		parsed[i].TokensOut = out / len(parsed)
	}
	return parsed, nil
}

func (c *OpenAIClient) GenerateSummary(ctx context.Context, req SummaryRequest) (SummaryResponse, error) {
	cfg, err := c.config(ctx)
	if err != nil {
		return SummaryResponse{}, err
	}
	var topB strings.Builder
	for i, iss := range req.TopIssues {
		fmt.Fprintf(&topB, "  %d. [%s] %s — %s\n", i+1, iss.Severity, iss.CheckID, iss.Title)
		if i >= 9 {
			break
		}
	}
	user := fmt.Sprintf(promptSummaryUser,
		coalesce(req.StoreName, "store"),
		coalesce(req.StoreURL, "(no url)"),
		req.Score, coalesce(req.RiskLevel, "unknown"),
		req.IssueCounts["critical"], req.IssueCounts["error"],
		req.IssueCounts["warning"], req.IssueCounts["info"],
		coalesce(req.StoreContext, "(none provided)"),
		topB.String(),
	)
	body, in, out, err := c.complete(ctx, cfg, promptSummarySystem, user)
	if err != nil {
		return SummaryResponse{}, err
	}
	summary, steps := parseSummary(body)
	return SummaryResponse{
		Summary:   summary,
		NextSteps: steps,
		TokensIn:  in,
		TokensOut: out,
		ModelUsed: cfg.Model,
	}, nil
}

// ----------------------------------------------------------------------------
// Internals
// ----------------------------------------------------------------------------

type providerConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

func (c *OpenAIClient) config(ctx context.Context) (providerConfig, error) {
	base, err1 := c.settings.Get(ctx, SettingBaseURL)
	key, err2 := c.settings.Get(ctx, SettingAPIKey)
	model, err3 := c.settings.Get(ctx, SettingModel)
	if err1 != nil || err2 != nil || err3 != nil || base == "" || key == "" || model == "" {
		return providerConfig{}, ErrNoConfig
	}
	return providerConfig{BaseURL: strings.TrimRight(base, "/"), APIKey: key, Model: model}, nil
}

// complete is the single retrying HTTP wrapper. Retries 429 and 5xx; bails on
// other 4xx (auth, request shape, model not found).
func (c *OpenAIClient) complete(ctx context.Context, cfg providerConfig, systemPrompt, userPrompt string) (string, int, int, error) {
	url := cfg.BaseURL + "/chat/completions"
	bodyBytes, err := json.Marshal(chatRequest{
		Model:       cfg.Model,
		Temperature: c.temperature,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	})
	if err != nil {
		return "", 0, 0, fmt.Errorf("ai: encode request: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		if attempt > 1 {
			delay := c.backoffBase * time.Duration(1<<(attempt-2)) // 1s, 2s, 4s
			select {
			case <-ctx.Done():
				return "", 0, 0, ctx.Err()
			case <-time.After(delay):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
		if err != nil {
			return "", 0, 0, err
		}
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		// Status routing.
		switch {
		case resp.StatusCode == 200:
			defer resp.Body.Close()
			respBody, err := io.ReadAll(resp.Body)
			if err != nil {
				return "", 0, 0, err
			}
			var parsed chatResponse
			if err := json.Unmarshal(respBody, &parsed); err != nil {
				return "", 0, 0, fmt.Errorf("ai: decode response: %w", err)
			}
			if len(parsed.Choices) == 0 {
				return "", 0, 0, ErrEmptyResponse
			}
			content := parsed.Choices[0].Message.Content
			c.logger.Info("ai_call",
				slog.String("provider", redactProviderHost(url)),
				slog.String("model", parsed.Model),
				slog.Int("tokens_in", parsed.Usage.PromptTokens),
				slog.Int("tokens_out", parsed.Usage.CompletionTokens),
				slog.Int("attempts", attempt),
			)
			return content, parsed.Usage.PromptTokens, parsed.Usage.CompletionTokens, nil

		case resp.StatusCode == 429 || resp.StatusCode >= 500:
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("ai: provider returned %d", resp.StatusCode)
			c.logger.Warn("ai_retry", slog.Int("status", resp.StatusCode), slog.Int("attempt", attempt))
			continue

		default:
			// 4xx that isn't 429: don't retry.
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			resp.Body.Close()
			return "", 0, 0, fmt.Errorf("ai: provider returned %d: %s",
				resp.StatusCode, strings.TrimSpace(string(respBody)))
		}
	}
	if lastErr == nil {
		lastErr = errors.New("ai: max attempts exceeded")
	}
	return "", 0, 0, lastErr
}

// ----------------------------------------------------------------------------
// Parsers + helpers
// ----------------------------------------------------------------------------

// parseFixBatch splits on "<<FIX N>>:" markers and returns one FixResponse per
// requested issue. Missing markers fall back to "(no rewrite produced)".
func parseFixBatch(body string, issues []FixRequest) []FixResponse {
	out := make([]FixResponse, len(issues))
	for i, iss := range issues {
		out[i] = FixResponse{IssueID: iss.IssueID, Suggested: "(no rewrite produced)"}
	}

	// Walk the body line-by-line and accumulate text per fix index.
	type chunk struct {
		index int
		text  strings.Builder
	}
	var current *chunk
	chunks := map[int]*chunk{}
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if n, rest, ok := matchFixMarker(trim); ok {
			c := &chunk{index: n}
			c.text.WriteString(rest)
			chunks[n] = c
			current = c
			continue
		}
		if current != nil && trim != "" {
			current.text.WriteString(" ")
			current.text.WriteString(trim)
		}
	}
	for n, c := range chunks {
		idx := n - 1
		if idx >= 0 && idx < len(out) {
			out[idx].Suggested = strings.TrimSpace(c.text.String())
		}
	}
	return out
}

// matchFixMarker matches "<<FIX N>>: rest" and returns (N, rest, true).
func matchFixMarker(line string) (int, string, bool) {
	const prefix = "<<FIX "
	if !strings.HasPrefix(line, prefix) {
		return 0, "", false
	}
	rest := line[len(prefix):]
	end := strings.Index(rest, ">>:")
	if end <= 0 {
		return 0, "", false
	}
	numStr := strings.TrimSpace(rest[:end])
	n := 0
	for _, r := range numStr {
		if r < '0' || r > '9' {
			return 0, "", false
		}
		n = n*10 + int(r-'0')
	}
	return n, strings.TrimSpace(rest[end+3:]), true
}

func parseSummary(body string) (string, []string) {
	var summary string
	var steps []string
	inSteps := false
	for _, line := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		if strings.HasPrefix(trim, "SUMMARY:") {
			summary = strings.TrimSpace(strings.TrimPrefix(trim, "SUMMARY:"))
			continue
		}
		if strings.HasPrefix(trim, "NEXT STEPS:") {
			inSteps = true
			continue
		}
		if inSteps {
			step := strings.TrimSpace(strings.TrimPrefix(trim, "-"))
			step = strings.TrimSpace(strings.TrimPrefix(step, "•"))
			step = strings.TrimSpace(strings.TrimPrefix(step, "*"))
			if step != "" {
				steps = append(steps, step)
			}
		}
	}
	if summary == "" {
		summary = strings.TrimSpace(body)
	}
	return summary, steps
}

func coalesce(a, b string) string {
	if a == "" {
		return b
	}
	return a
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// redactProviderHost extracts just the host from a base URL so logs don't
// reveal the path or query but DO reveal which provider was called.
func redactProviderHost(rawURL string) string {
	i := strings.Index(rawURL, "://")
	if i < 0 {
		return rawURL
	}
	rest := rawURL[i+3:]
	if j := strings.IndexAny(rest, "/?"); j >= 0 {
		return rest[:j]
	}
	return rest
}

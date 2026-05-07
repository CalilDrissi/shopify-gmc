package settings

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type countingBackend struct {
	*MemBackend
	mu        sync.Mutex
	gets      int
	sets      int
	deletes   int
	getErr    error
}

func newCountingBackend() *countingBackend {
	return &countingBackend{MemBackend: NewMemBackend()}
}

func (c *countingBackend) Get(ctx context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	c.gets++
	err := c.getErr
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return c.MemBackend.Get(ctx, key)
}

func (c *countingBackend) Set(ctx context.Context, key string, v []byte) error {
	c.mu.Lock()
	c.sets++
	c.mu.Unlock()
	return c.MemBackend.Set(ctx, key, v)
}

func (c *countingBackend) Delete(ctx context.Context, key string) error {
	c.mu.Lock()
	c.deletes++
	c.mu.Unlock()
	return c.MemBackend.Delete(ctx, key)
}

func (c *countingBackend) Counts() (gets, sets, deletes int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gets, c.sets, c.deletes
}

type fakeAuditor struct {
	mu      sync.Mutex
	entries []auditEntry
}

type auditEntry struct {
	AdminUserID *uuid.UUID
	Key         string
	Preview     string
}

func (f *fakeAuditor) LogSettingChange(_ context.Context, id *uuid.UUID, key, preview string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, auditEntry{AdminUserID: id, Key: key, Preview: preview})
	return nil
}

func (f *fakeAuditor) Entries() []auditEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]auditEntry(nil), f.entries...)
}

func newTestService(t *testing.T, env map[string]string, clock *time.Time) (*Service, *countingBackend, *fakeAuditor) {
	t.Helper()
	c := newTestCipher(t)
	back := newCountingBackend()
	aud := &fakeAuditor{}
	envFn := func(k string) string { return env[k] }
	clockFn := time.Now
	if clock != nil {
		clockFn = func() time.Time { return *clock }
	}
	svc := New(c, back, aud,
		WithEnv(envFn),
		WithClock(clockFn),
		WithCacheTTL(5*time.Minute),
	)
	return svc, back, aud
}

func TestService_SetGetRoundTrip(t *testing.T) {
	t.Parallel()
	svc, back, aud := newTestService(t, nil, nil)
	ctx := context.Background()

	admin := uuid.New()
	if err := svc.Set(ctx, &admin, KeyAIBaseURL, "https://api.anthropic.com"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := svc.Get(ctx, KeyAIBaseURL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "https://api.anthropic.com" {
		t.Errorf("got %q, want %q", got, "https://api.anthropic.com")
	}

	_, sets, _ := back.Counts()
	if sets != 1 {
		t.Errorf("backend sets=%d, want 1", sets)
	}

	if len(aud.Entries()) != 1 {
		t.Fatalf("got %d audit entries, want 1", len(aud.Entries()))
	}
	e := aud.Entries()[0]
	if e.Key != KeyAIBaseURL {
		t.Errorf("audit key=%q want %q", e.Key, KeyAIBaseURL)
	}
	if e.Preview != "https://api.anthropic.com" {
		t.Errorf("audit preview=%q, want non-secret value", e.Preview)
	}
	if e.AdminUserID == nil || *e.AdminUserID != admin {
		t.Errorf("admin id mismatch: %v", e.AdminUserID)
	}
}

func TestService_AuditNeverContainsSecretValue(t *testing.T) {
	t.Parallel()
	svc, _, aud := newTestService(t, nil, nil)
	ctx := context.Background()

	const secret = "sk-very-sensitive-1234567890"
	if err := svc.Set(ctx, nil, KeyAIAPIKey, secret); err != nil {
		t.Fatalf("Set: %v", err)
	}
	for _, e := range aud.Entries() {
		if e.Preview == secret {
			t.Fatalf("audit preview leaked secret value")
		}
		if e.Preview != maskDisplay {
			t.Errorf("preview=%q, want mask %q", e.Preview, maskDisplay)
		}
	}
}

func TestService_CacheTTL_HitMiss(t *testing.T) {
	t.Parallel()
	clk := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	svc, back, _ := newTestService(t, nil, &clk)
	ctx := context.Background()

	_ = svc.Set(ctx, nil, KeyAIBaseURL, "https://x")

	_, _ = svc.Get(ctx, KeyAIBaseURL)
	gets1, _, _ := back.Counts()
	_, _ = svc.Get(ctx, KeyAIBaseURL)
	gets2, _, _ := back.Counts()

	if gets2 != gets1 {
		t.Errorf("expected cache hit; backend gets jumped %d -> %d", gets1, gets2)
	}

	clk = clk.Add(6 * time.Minute)
	_, _ = svc.Get(ctx, KeyAIBaseURL)
	gets3, _, _ := back.Counts()
	if gets3 != gets2+1 {
		t.Errorf("expected cache miss after TTL; backend gets %d -> %d", gets2, gets3)
	}
}

func TestService_CacheInvalidatedOnSet(t *testing.T) {
	t.Parallel()
	clk := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	svc, back, _ := newTestService(t, nil, &clk)
	ctx := context.Background()

	_ = svc.Set(ctx, nil, KeyAIBaseURL, "https://old")
	_, _ = svc.Get(ctx, KeyAIBaseURL)
	getsBefore, _, _ := back.Counts()

	_ = svc.Set(ctx, nil, KeyAIBaseURL, "https://new")
	got, _ := svc.Get(ctx, KeyAIBaseURL)
	if got != "https://new" {
		t.Errorf("got %q after Set, want updated value", got)
	}
	getsAfter, _, _ := back.Counts()
	if getsAfter <= getsBefore {
		t.Errorf("expected backend Get after Set invalidates cache; %d -> %d", getsBefore, getsAfter)
	}
}

func TestService_EnvFallback(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"AI_BASE_URL": "https://from-env.example.com",
	}
	svc, _, _ := newTestService(t, env, nil)
	ctx := context.Background()

	got, err := svc.Get(ctx, KeyAIBaseURL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "https://from-env.example.com" {
		t.Errorf("got %q, want env value", got)
	}

	prev, err := svc.Preview(ctx, KeyAIBaseURL)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if prev.Source != SourceEnv {
		t.Errorf("source=%q want %q", prev.Source, SourceEnv)
	}
	if !prev.Set {
		t.Error("Set should be true when env supplies the value")
	}
}

func TestService_DBOverridesEnv(t *testing.T) {
	t.Parallel()
	env := map[string]string{"AI_BASE_URL": "https://from-env"}
	svc, _, _ := newTestService(t, env, nil)
	ctx := context.Background()

	if err := svc.Set(ctx, nil, KeyAIBaseURL, "https://from-db"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, _ := svc.Get(ctx, KeyAIBaseURL)
	if got != "https://from-db" {
		t.Errorf("got %q, want db value", got)
	}
	prev, _ := svc.Preview(ctx, KeyAIBaseURL)
	if prev.Source != SourceDB {
		t.Errorf("source=%q want %q", prev.Source, SourceDB)
	}
}

func TestService_PreviewMasksSecrets(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t, nil, nil)
	ctx := context.Background()

	cases := []struct {
		name    string
		key     string
		value   string
		want    string
		wantSet bool
	}{
		{"unset-non-secret", KeyAIBaseURL, "", unsetDisplay, false},
		{"set-non-secret", KeyAIModel, "claude-sonnet-4-6", "claude-sonnet-4-6", true},
		{"set-secret-masked", KeyAIAPIKey, "sk-supersecret", maskDisplay, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.value != "" {
				if err := svc.Set(ctx, nil, tc.key, tc.value); err != nil {
					t.Fatalf("Set: %v", err)
				}
			}
			p, err := svc.Preview(ctx, tc.key)
			if err != nil {
				t.Fatalf("Preview: %v", err)
			}
			if p.Display != tc.want {
				t.Errorf("display=%q want %q", p.Display, tc.want)
			}
			if p.Set != tc.wantSet {
				t.Errorf("set=%v want %v", p.Set, tc.wantSet)
			}
		})
	}
}

func TestService_GetAllReturnsAllRegistered(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t, nil, nil)
	ctx := context.Background()

	previews := svc.GetAll(ctx)
	if len(previews) != len(DefaultRegistry()) {
		t.Errorf("len=%d want %d", len(previews), len(DefaultRegistry()))
	}
	keys := map[string]bool{}
	for _, p := range previews {
		keys[p.Key] = true
	}
	for _, def := range DefaultRegistry() {
		if !keys[def.Key] {
			t.Errorf("missing key %q in GetAll", def.Key)
		}
	}
}

func TestService_NotRegisteredErrors(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t, nil, nil)
	ctx := context.Background()

	if _, err := svc.Get(ctx, "unknown_key"); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("Get unknown: got %v want ErrNotRegistered", err)
	}
	if err := svc.Set(ctx, nil, "unknown_key", "x"); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("Set unknown: got %v want ErrNotRegistered", err)
	}
	if _, err := svc.Preview(ctx, "unknown_key"); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("Preview unknown: got %v want ErrNotRegistered", err)
	}
}

func TestService_GetUnsetReturnsErrUnset(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t, nil, nil)
	if _, err := svc.Get(context.Background(), KeyAIAPIKey); !errors.Is(err, ErrUnset) {
		t.Errorf("got %v want ErrUnset", err)
	}
}

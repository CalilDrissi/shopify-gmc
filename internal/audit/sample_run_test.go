package audit_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/example/gmcauditor/internal/ai"
	"github.com/example/gmcauditor/internal/audit"
	_ "github.com/example/gmcauditor/internal/audit/checks"
)

// TestPipelineSampleRun is a "documenting" test: runs the pipeline once
// against the fixture context with mocked AI and t.Log()s a readable dump of
// the AuditOutput. Run with -v to see it.
func TestPipelineSampleRun(t *testing.T) {
	t.Parallel()
	cx := buildFixtureContext(t)
	p := &audit.Pipeline{
		Crawl: func(ctx context.Context, _ string) (audit.CheckContext, error) { return cx, nil },
		AI:    ai.NewMockClient(),
		Now:   func() time.Time { return time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC) },
	}
	out, err := p.Run(context.Background(), audit.AuditInput{
		AuditID:      uuid.MustParse("aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa"),
		TenantID:     uuid.New(),
		StoreID:      uuid.New(),
		StoreURL:     "https://acme.myshopify.com",
		StoreName:    "Acme Goods",
		StoreContext: "Bay Area textile brand selling organic cotton aprons.",
		Trigger:      "manual",
	})
	if err != nil {
		t.Fatalf("pipeline run: %v", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n────── audit run ──────\n")
	fmt.Fprintf(&b, "AuditID:    %s\n", out.AuditID)
	fmt.Fprintf(&b, "Score:      %d / 100   risk=%s\n", out.Score, out.RiskLevel)
	fmt.Fprintf(&b, "Counts:     critical=%d  error=%d  warning=%d  info=%d\n",
		out.Counts["critical"], out.Counts["error"], out.Counts["warning"], out.Counts["info"])
	fmt.Fprintf(&b, "Stages (%d):\n", len(out.Stages))
	for _, s := range out.Stages {
		fmt.Fprintf(&b, "  %-10s %-8s  %s\n", s.Name, s.Status, s.Detail)
	}
	fmt.Fprintf(&b, "Categories:\n")
	for _, c := range out.Categories {
		fmt.Fprintf(&b, "  %-15s pass=%-2d fail=%-2d worst_severity=%s\n",
			c.Category, c.Pass, c.Fail, c.WorstSeverity)
	}
	fmt.Fprintf(&b, "Failed checks (%d):\n", failedCount(out))
	for _, r := range out.Results {
		if r.Status != audit.StatusFail {
			continue
		}
		fmt.Fprintf(&b, "  ✗ %-30s [%-7s]  %d issues\n", r.Meta.ID, r.Severity, len(r.Issues))
	}
	fmt.Fprintf(&b, "Sample suggestions:\n")
	i := 0
	for k, v := range out.Suggestions {
		if i >= 3 {
			break
		}
		fmt.Fprintf(&b, "  %s → %s\n", k, truncateForDump(v, 90))
		i++
	}
	fmt.Fprintf(&b, "Summary:    %s\n", out.Summary)
	fmt.Fprintf(&b, "Next steps:\n")
	for _, s := range out.NextSteps {
		fmt.Fprintf(&b, "  - %s\n", s)
	}
	fmt.Fprintf(&b, "──────────────────────\n")
	t.Log(b.String())

	// Spot-checks so this doubles as a real test.
	// 8 = 7 original + the GMC sync stage that was added when the GMC
	// integration landed. Skipped when no GMCSyncFn is configured.
	if len(out.Stages) != 8 {
		t.Errorf("stages = %d, want 8", len(out.Stages))
	}
	if len(out.Suggestions) == 0 {
		t.Error("expected at least one AI suggestion (or fallback)")
	}
	// The mocked-AI summary path always populates next steps.
	if len(out.NextSteps) == 0 {
		t.Error("expected next steps from mock summary")
	}

	// Ensure the output is JSON-serialisable end-to-end (we'll need this to
	// stash on the audits row).
	if _, err := json.Marshal(out); err != nil {
		t.Errorf("output not json-serialisable: %v", err)
	}
}

func failedCount(out *audit.AuditOutput) int {
	n := 0
	for _, r := range out.Results {
		if r.Status == audit.StatusFail {
			n++
		}
	}
	return n
}

func truncateForDump(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

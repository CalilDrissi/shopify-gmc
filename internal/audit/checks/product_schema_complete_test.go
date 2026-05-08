package checks

import (
	"context"
	"strings"
	"testing"

	"github.com/example/gmcauditor/internal/audit"
)

func TestProductSchemaComplete(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name              string
		fixtures          []string
		wantStatus        audit.Status
		wantIssueCount    int
		wantSubstringInDetail string
	}{
		{
			name:           "all fields present",
			fixtures:       []string{"products/complete.html"},
			wantStatus:     audit.StatusPass,
			wantIssueCount: 0,
		},
		{
			name:                  "missing brand and availability",
			fixtures:              []string{"products/missing_brand_and_availability.html"},
			wantStatus:            audit.StatusFail,
			wantIssueCount:        1,
			wantSubstringInDetail: "brand",
		},
		{
			name:                  "missing brand and availability detail mentions availability",
			fixtures:              []string{"products/missing_brand_and_availability.html"},
			wantStatus:            audit.StatusFail,
			wantIssueCount:        1,
			wantSubstringInDetail: "offers.availability",
		},
		{
			name:           "no schema → not this check's job (passes)",
			fixtures:       []string{"products/no_schema.html"},
			wantStatus:     audit.StatusPass,
			wantIssueCount: 0,
		},
		{
			name: "mixed: complete + incomplete reports the bad one",
			fixtures: []string{
				"products/complete.html",
				"products/missing_brand_and_availability.html",
			},
			wantStatus:     audit.StatusFail,
			wantIssueCount: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cx := audit.CheckContext{StoreURL: "https://acme.myshopify.com"}
			for i, f := range tc.fixtures {
				cx.ProductPages = append(cx.ProductPages,
					loadFixturePage(t, f, "https://acme.myshopify.com/products/p"+itoa(i)))
			}
			res := runProductSchemaComplete(context.Background(), cx)

			mustHaveStatus(t, res.Status, tc.wantStatus)
			if len(res.Issues) != tc.wantIssueCount {
				t.Errorf("issue count = %d, want %d (issues=%+v)", len(res.Issues), tc.wantIssueCount, res.Issues)
			}
			if tc.wantSubstringInDetail != "" {
				found := false
				for _, iss := range res.Issues {
					if strings.Contains(iss.Detail, tc.wantSubstringInDetail) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("no issue mentioned %q in Detail; got %+v", tc.wantSubstringInDetail, res.Issues)
				}
			}
		})
	}
}

func TestProductSchemaComplete_Meta(t *testing.T) {
	t.Parallel()
	c, ok := audit.Get(metaProductSchemaComplete.ID)
	if !ok {
		t.Fatalf("check %q not registered", metaProductSchemaComplete.ID)
	}
	if c.Meta.DefaultSeverity != audit.SeverityError {
		t.Errorf("severity = %v, want error", c.Meta.DefaultSeverity)
	}
	if !c.Meta.AIFixEligible {
		t.Errorf("expected AIFixEligible=true for product schema")
	}
}

func TestProductSchemaComplete_Instructions(t *testing.T) {
	t.Parallel()
	fi := instructionsProductSchemaComplete()
	if fi.Difficulty == "" || fi.TimeEstimate == "" || fi.Summary == "" {
		t.Error("missing core copy")
	}
	if len(fi.Steps) < 3 {
		t.Errorf("expected at least 3 steps, got %d", len(fi.Steps))
	}
	for i, s := range fi.Steps {
		if s.Number != i+1 {
			t.Errorf("step %d: Number=%d, want %d", i, s.Number, i+1)
		}
		if s.Action == "" {
			t.Errorf("step %d has empty action", i+1)
		}
	}
}

func itoa(i int) string {
	switch i {
	case 0:
		return "0"
	case 1:
		return "1"
	case 2:
		return "2"
	}
	return "n"
}

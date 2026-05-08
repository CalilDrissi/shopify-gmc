package differ

import "testing"

func TestCompute_FirstAudit(t *testing.T) {
	curr := []IssueKey{
		{CheckID: "https_everywhere", PageURL: "http://x", Severity: "critical", Title: "HTTPS"},
		{CheckID: "title_quality", PageURL: "/p/1", Severity: "warning", Title: "Title"},
	}
	d := Compute(nil, curr, nil, 60)
	if d.NewCount != 2 {
		t.Errorf("NewCount=%d want 2", d.NewCount)
	}
	if d.NewCriticalCount != 1 {
		t.Errorf("NewCriticalCount=%d want 1", d.NewCriticalCount)
	}
	if d.ResolvedCount != 0 || d.UnchangedCount != 0 {
		t.Errorf("expected only new issues, got %+v", d)
	}
	if d.ScoreDelta != 0 {
		t.Errorf("ScoreDelta=%d want 0 (no prev)", d.ScoreDelta)
	}
	if d.PrevScore != nil {
		t.Errorf("PrevScore should be nil for first audit")
	}
	if d.NewScore != 60 {
		t.Errorf("NewScore=%d want 60", d.NewScore)
	}
}

func TestCompute_MixedDiff(t *testing.T) {
	prev := []IssueKey{
		{CheckID: "a", PageURL: "/", Severity: "critical"}, // resolved
		{CheckID: "b", PageURL: "/", Severity: "warning"},  // unchanged
	}
	curr := []IssueKey{
		{CheckID: "b", PageURL: "/", Severity: "warning"}, // unchanged
		{CheckID: "c", PageURL: "/", Severity: "critical"}, // new
		{CheckID: "c", PageURL: "/x", Severity: "error"},   // new (different page)
	}
	prevScore := 70
	d := Compute(prev, curr, &prevScore, 55)
	if d.NewCount != 2 {
		t.Errorf("NewCount=%d want 2", d.NewCount)
	}
	if d.NewCriticalCount != 1 {
		t.Errorf("NewCriticalCount=%d want 1", d.NewCriticalCount)
	}
	if d.ResolvedCount != 1 {
		t.Errorf("ResolvedCount=%d want 1", d.ResolvedCount)
	}
	if d.UnchangedCount != 1 {
		t.Errorf("UnchangedCount=%d want 1", d.UnchangedCount)
	}
	if d.ScoreDelta != -15 {
		t.Errorf("ScoreDelta=%d want -15", d.ScoreDelta)
	}
	if len(d.NewIssues) != 2 {
		t.Errorf("NewIssues=%d want 2", len(d.NewIssues))
	}
	if len(d.ResolvedIssues) != 1 {
		t.Errorf("ResolvedIssues=%d want 1", len(d.ResolvedIssues))
	}
}

func TestCompute_AllResolved(t *testing.T) {
	prev := []IssueKey{
		{CheckID: "a", PageURL: "/", Severity: "critical"},
		{CheckID: "b", PageURL: "/", Severity: "warning"},
	}
	prevScore := 30
	d := Compute(prev, nil, &prevScore, 100)
	if d.ResolvedCount != 2 {
		t.Errorf("ResolvedCount=%d want 2", d.ResolvedCount)
	}
	if d.NewCount != 0 {
		t.Errorf("NewCount=%d want 0", d.NewCount)
	}
	if d.ScoreDelta != 70 {
		t.Errorf("ScoreDelta=%d want +70", d.ScoreDelta)
	}
}

func TestCompute_SamePageDifferentCheckIsNew(t *testing.T) {
	prev := []IssueKey{
		{CheckID: "a", PageURL: "/p/1", Severity: "warning"},
	}
	curr := []IssueKey{
		{CheckID: "a", PageURL: "/p/1", Severity: "warning"}, // unchanged
		{CheckID: "b", PageURL: "/p/1", Severity: "error"},   // new (different rule)
	}
	d := Compute(prev, curr, nil, 90)
	if d.NewCount != 1 || d.UnchangedCount != 1 || d.ResolvedCount != 0 {
		t.Errorf("identity should be (rule, page); got %+v", d)
	}
}

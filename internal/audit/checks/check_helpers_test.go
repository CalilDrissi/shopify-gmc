package checks

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/example/gmcauditor/internal/audit"
)

// repoRoot resolves the path to the project root from a test file in
// internal/audit/checks/ — used to load fixtures under testdata/shopify.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile = .../internal/audit/checks/check_helpers_test.go
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
}

func loadFixturePage(t *testing.T, relpath, urlForPage string) *audit.Page {
	t.Helper()
	full := filepath.Join(repoRoot(t), "testdata", "shopify", relpath)
	b, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("read fixture %s: %v", relpath, err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("parse fixture %s: %v", relpath, err)
	}
	return &audit.Page{
		URL:        urlForPage,
		StatusCode: 200,
		HTML:       string(b),
		Doc:        doc,
		Headers:    http.Header{},
		FetchedAt:  time.Now(),
	}
}

func mustHaveStatus(t *testing.T, got audit.Status, want audit.Status) {
	t.Helper()
	if got != want {
		t.Errorf("status = %s, want %s", got, want)
	}
}

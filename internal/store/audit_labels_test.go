package store

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every action the code records must have a Spanish name in the panel's
// Activity view; otherwise a merchant reads raw codes such as "category.reorder".
func TestEveryAuditActionHasAPanelName(t *testing.T) {
	root := filepath.Join("..", "..")
	ui, err := os.ReadFile(filepath.Join(root, "web", "src", "pages", "reports", "Ops.tsx"))
	if err != nil {
		t.Fatal(err)
	}
	named := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^  "([a-z_.]+)": "`).FindAllStringSubmatch(string(ui), -1) {
		named[m[1]] = true
	}
	// Audit(ctx, tx, tenant, actor, "literal.action", ...): the fifth argument.
	call := regexp.MustCompile(`Audit\([^,()]+,\s*[^,()]+,\s*[^,()]+,\s*[^,()]+(?:\(\))?,\s*"([a-z_]+\.[a-z_.]+)"\s*,`)
	seen := 0
	_ = filepath.WalkDir(filepath.Join(root, "internal"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, _ := os.ReadFile(p)
		for _, m := range call.FindAllStringSubmatch(string(b), -1) {
			seen++
			if !named[m[1]] {
				t.Errorf("%s records %q, which has no name in web/src/pages/reports/Ops.tsx", p, m[1])
			}
		}
		return nil
	})
	if seen < 50 {
		t.Fatalf("found only %d audit calls; the scan is broken", seen)
	}
}

// Package archtest enforces the dependency rules of the GUI design (spec §2).
package archtest

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const mod = "github.com/raptordnj/goti"

type pkg struct {
	ImportPath string
	Imports    []string
}

func load(t *testing.T) []pkg {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	root := filepath.Dir(strings.TrimSpace(string(out)))
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Env = os.Environ()
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, stderr.String())
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	var pkgs []pkg
	for dec.More() {
		var p pkg
		if err := dec.Decode(&p); err != nil {
			t.Fatal(err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

func rel(p string) string { return strings.TrimPrefix(strings.TrimPrefix(p, mod), "/") }

func under(p, dir string) bool { return p == dir || strings.HasPrefix(p, dir+"/") }

func TestImportRules(t *testing.T) {
	pkgs := load(t)
	if len(pkgs) < 10 {
		t.Fatalf("suspiciously few packages: %d", len(pkgs))
	}
	gioAllowed := func(r string) bool {
		return r == "internal/ui" || r == "cmd/goti" ||
			under(r, "internal/ui/theme") || under(r, "internal/ui/components") ||
			under(r, "internal/ui/screens") || under(r, "internal/ui/dialogs") ||
			under(r, "internal/ui/icons") || under(r, "internal/ui/anim") ||
			under(r, "internal/ui/snapshot")
	}
	noToolkit := []string{"internal/ui/state", "internal/app", "internal/services", "internal/settings"}
	engine := []string{"domain", "downloader", "strategy", "transport", "storage", "persistence", "ratelimit", "events", "config"}
	forbiddenForEngine := []string{"internal/app", "internal/services", "internal/ui", "internal/tray"}

	for _, p := range pkgs {
		r := rel(p.ImportPath)
		for _, imp := range p.Imports {
			ri := rel(imp)
			isGio := strings.HasPrefix(imp, "gioui.org")
			isTray := strings.HasPrefix(imp, "fyne.io/systray")
			if isGio && !gioAllowed(r) {
				t.Errorf("%s imports Gio (%s)", r, imp)
			}
			if isTray && r != "internal/tray" {
				t.Errorf("%s imports systray (%s)", r, imp)
			}
			for _, n := range noToolkit {
				if r == n && (isGio || isTray) {
					t.Errorf("%s must not import %s", r, imp)
				}
			}
			if under(r, "internal/ui") {
				switch {
				case ri == "internal/downloader", ri == "internal/persistence", imp == "net/http":
					t.Errorf("UI package %s imports %s", r, imp)
				case ri == "internal/transport" && r != "internal/ui/state":
					t.Errorf("UI package %s imports transport (only ui/state may)", r)
				}
			}
			for _, e := range engine {
				if r == "internal/"+e {
					for _, f := range forbiddenForEngine {
						if under(ri, f) {
							t.Errorf("engine package %s imports %s", r, ri)
						}
					}
				}
			}
		}
	}
}

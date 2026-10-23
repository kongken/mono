package moduleboundary_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Each service binary must only depend on its own business tree and platform:
// no business tree may reach into another, and the BFF reaches products only
// through their proto contracts.
var boundaries = map[string][]string{
	"github.com/kongken/mono/cmd/bff": {
		"github.com/kongken/mono/internal/tripmap",
		"github.com/kongken/mono/internal/metro",
	},
	"github.com/kongken/mono/cmd/tripmap": {
		"github.com/kongken/mono/internal/bff",
		"github.com/kongken/mono/internal/metro",
	},
	"github.com/kongken/mono/cmd/metro": {
		"github.com/kongken/mono/internal/bff",
		"github.com/kongken/mono/internal/tripmap",
	},
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func TestServiceImportBoundaries(t *testing.T) {
	root := moduleRoot(t)
	checked := 0
	for entry, forbiddenTrees := range boundaries {
		// Service images only copy their own cmd/ and business tree, so the other
		// service's entry point is absent there; the image build itself enforces
		// the boundary in that case.
		dir := filepath.Join(root, "cmd", filepath.Base(entry))
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		checked++
		cmd := exec.Command("go", "list", "-buildvcs=false", "-deps", "-f", "{{.ImportPath}}", entry)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			var stderr []byte
			if ee, ok := err.(*exec.ExitError); ok {
				stderr = ee.Stderr
			}
			t.Fatalf("go list -deps %s: %v\n%s", entry, err, stderr)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			for _, forbidden := range forbiddenTrees {
				if line == forbidden || strings.HasPrefix(line, forbidden+"/") {
					t.Fatalf("%s must not import %s (found %s)", entry, forbidden, line)
				}
			}
		}
	}
	if checked == 0 {
		t.Skip("no service entry point present")
	}
}

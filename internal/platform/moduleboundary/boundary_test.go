package moduleboundary_test

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Each service binary must only depend on its own business tree and platform.
var boundaries = map[string]string{
	"github.com/kongken/mono/cmd/bff":     "github.com/kongken/mono/internal/tripmap",
	"github.com/kongken/mono/cmd/tripmap": "github.com/kongken/mono/internal/bff",
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
	for entry, forbidden := range boundaries {
		cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", entry)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list -deps %s: %v", entry, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line == forbidden || strings.HasPrefix(line, forbidden+"/") {
				t.Fatalf("%s must not import %s (found %s)", entry, forbidden, line)
			}
		}
	}
}

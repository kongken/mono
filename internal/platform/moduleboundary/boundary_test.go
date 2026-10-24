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
		"github.com/kongken/mono/internal/usersrv",
	},
	"github.com/kongken/mono/cmd/tripmap": {
		"github.com/kongken/mono/internal/bff",
		"github.com/kongken/mono/internal/metro",
		"github.com/kongken/mono/internal/usersrv",
	},
	"github.com/kongken/mono/cmd/metro": {
		"github.com/kongken/mono/internal/bff",
		"github.com/kongken/mono/internal/tripmap",
		"github.com/kongken/mono/internal/usersrv",
	},
	"github.com/kongken/mono/cmd/usersrv": {
		"github.com/kongken/mono/internal/bff",
		"github.com/kongken/mono/internal/tripmap",
		"github.com/kongken/mono/internal/metro",
	},
	// The operator tool is part of the usersrv image tree and has the same limits.
	"github.com/kongken/mono/cmd/usersrv-admin": {
		"github.com/kongken/mono/internal/bff",
		"github.com/kongken/mono/internal/tripmap",
		"github.com/kongken/mono/internal/metro",
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

var businessTrees = []string{"bff", "tripmap", "metro", "usersrv"}

// depsOf lists the import paths a package pattern depends on (tests excluded).
func depsOf(t *testing.T, pattern string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-buildvcs=false", "-deps", "-f", "{{.ImportPath}}", pattern)
	cmd.Dir = moduleRoot(t)
	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		t.Fatalf("go list -deps %s: %v\n%s", pattern, err, stderr)
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

func hasTree(path, tree string) bool {
	root := "github.com/kongken/mono/internal/" + tree
	return path == root || strings.HasPrefix(path, root+"/")
}

// A business tree never imports another business tree, in any of its packages,
// not only along the path to its entry point: services reach each other through
// generated proto contracts only.
func TestBusinessTreesDoNotImportEachOther(t *testing.T) {
	root := moduleRoot(t)
	for _, tree := range businessTrees {
		dir := filepath.Join(root, "internal", tree)
		if _, err := os.Stat(dir); err != nil {
			continue // service images copy only their own tree
		}
		for _, dep := range depsOf(t, "./internal/"+tree+"/...") {
			for _, other := range businessTrees {
				if other != tree && hasTree(dep, other) {
					t.Errorf("internal/%s must not import internal/%s (found %s)", tree, other, dep)
				}
			}
		}
	}
}

// internal/platform is shared by every service and so depends on none of them.
func TestPlatformDoesNotDependOnBusinessTrees(t *testing.T) {
	if _, err := os.Stat(filepath.Join(moduleRoot(t), "internal", "platform")); err != nil {
		t.Skip("no platform tree")
	}
	for _, dep := range depsOf(t, "./internal/platform/...") {
		for _, tree := range businessTrees {
			if hasTree(dep, tree) {
				t.Errorf("internal/platform must not import internal/%s (found %s)", tree, dep)
			}
		}
	}
}

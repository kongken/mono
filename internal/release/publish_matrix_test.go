package release_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The Docker images workflow is data, not code: nothing compiles it and a green run only says the
// images it happened to build worked. On 2026-11-08 the Timeline change added its two matrix entries
// by replacing the BFF entry instead of appending, so the workflow stayed green while `mono-bff` was
// no longer built or published at all. `matrixProblems` below is the structural regression: it reads
// the real `jobs.build.strategy.matrix.include` from the workflow file (a `bff` string in a comment or
// a build arg does not count), and this test runs the same check over the file's actual matrix plus
// mutations of it that must fail, so the guard cannot pass by agreeing with itself.

// publishedMatrix is every image the monorepo publishes: each backend service and each application,
// with the registry suffix and Dockerfile the workflow must use. A new image is a deliberate change to
// this list; a matrix entry that is missing from it (or an unexpected one in the file) fails.
var publishedMatrix = []matrixEntry{
	{Image: "tripmap", Suffix: "-tripmap", Dockerfile: "deploy/tripmap/Dockerfile"},
	{Image: "usersrv", Suffix: "-usersrv", Dockerfile: "deploy/usersrv/Dockerfile"},
	{Image: "trip-map-web", Suffix: "-trip-map-web", Dockerfile: "deploy/trip-map-web/Dockerfile"},
	{Image: "bff", Suffix: "-bff", Dockerfile: "deploy/bff/Dockerfile"},
	{Image: "timeline", Suffix: "-timeline", Dockerfile: "deploy/timeline/Dockerfile"},
	{Image: "timeline-web", Suffix: "-timeline-web", Dockerfile: "deploy/timeline-web/Dockerfile"},
	{Image: "metro", Suffix: "-metro", Dockerfile: "deploy/metro/Dockerfile"},
	{Image: "metro-web", Suffix: "-metro-web", Dockerfile: "deploy/metro-web/Dockerfile"},
	{Image: "ozid-web", Suffix: "-ozid-web", Dockerfile: "deploy/ozid-web/Dockerfile"},
	{Image: "admin-web", Suffix: "-admin-web", Dockerfile: "deploy/admin-web/Dockerfile"},
}

// matrixEntry is one `include:` item. Fields this check does not review (build_args) are ignored.
type matrixEntry struct {
	Image      string `yaml:"image"`
	Suffix     string `yaml:"suffix"`
	Dockerfile string `yaml:"dockerfile"`
	BuildArgs  string `yaml:"build_args"`
}

type dockerWorkflow struct {
	Jobs map[string]struct {
		Strategy struct {
			Matrix struct {
				Include []matrixEntry `yaml:"include"`
			} `yaml:"matrix"`
		} `yaml:"strategy"`
	} `yaml:"jobs"`
}

// unmarshalMatrix reads the publication matrix out of a workflow document. It returns an error rather
// than a test failure so the negative cases can reuse it on documents that are wrong on purpose.
func unmarshalMatrix(src []byte) ([]matrixEntry, error) {
	var wf dockerWorkflow
	if err := yaml.Unmarshal(src, &wf); err != nil {
		return nil, fmt.Errorf("parse docker-publish workflow: %w", err)
	}
	job, ok := wf.Jobs["build"]
	if !ok {
		return nil, fmt.Errorf("docker-publish workflow has no build job")
	}
	entries := job.Strategy.Matrix.Include
	if len(entries) == 0 {
		return nil, fmt.Errorf("build job has no strategy.matrix.include entries")
	}
	return entries, nil
}

// matrixProblems reports every way the matrix differs from publishedMatrix: a missing, repeated or
// substituted image, a wrong suffix or Dockerfile, a Dockerfile that is not in the repository, and a
// publication target (suffix) or Dockerfile reused by another image. It reads entries only, so text
// elsewhere in the file can never satisfy it.
func matrixProblems(entries []matrixEntry, dockerfileExists func(string) bool) []string {
	var problems []string
	byImage := map[string][]matrixEntry{}
	targets := map[string]string{}
	dockerfiles := map[string]string{}
	for _, entry := range entries {
		if entry.Image == "" || entry.Suffix == "" || entry.Dockerfile == "" {
			problems = append(problems, fmt.Sprintf("matrix entry %q must name image, suffix and dockerfile", entry.Image))
			continue
		}
		byImage[entry.Image] = append(byImage[entry.Image], entry)
		if prev, ok := targets[entry.Suffix]; ok {
			problems = append(problems, fmt.Sprintf("publication target %q is used by both %q and %q; a new image must not take over an existing target", entry.Suffix, prev, entry.Image))
		} else {
			targets[entry.Suffix] = entry.Image
		}
		if prev, ok := dockerfiles[entry.Dockerfile]; ok && prev != entry.Image {
			problems = append(problems, fmt.Sprintf("Dockerfile %q is built for both %q and %q", entry.Dockerfile, prev, entry.Image))
		} else {
			dockerfiles[entry.Dockerfile] = entry.Image
		}
		if want := "-" + entry.Image; entry.Suffix != want {
			problems = append(problems, fmt.Sprintf("image %q publishes as %q, want %q", entry.Image, entry.Suffix, want))
		}
		if want := "deploy/" + entry.Image + "/Dockerfile"; entry.Dockerfile != want {
			problems = append(problems, fmt.Sprintf("image %q builds %q, want %q", entry.Image, entry.Dockerfile, want))
		}
		if !dockerfileExists(entry.Dockerfile) {
			problems = append(problems, fmt.Sprintf("image %q builds %q, which is not in the repository", entry.Image, entry.Dockerfile))
		}
	}

	// The BFF is the regression this test exists for: exactly one entry, with the reviewed suffix and
	// Dockerfile (the exact values are checked with the rest of publishedMatrix below).
	if found := len(byImage["bff"]); found != 1 {
		problems = append(problems, fmt.Sprintf("BFF publication regression: expected exactly one bff image, found %d", found))
	}

	expected := map[string]matrixEntry{}
	for _, want := range publishedMatrix {
		expected[want.Image] = want
	}
	for _, want := range publishedMatrix {
		found := byImage[want.Image]
		if len(found) == 0 {
			problems = append(problems, fmt.Sprintf("missing image %q: every product keeps its own matrix entry, a new product must be added rather than substituted", want.Image))
			continue
		}
		for _, entry := range found {
			if entry.Suffix != want.Suffix || entry.Dockerfile != want.Dockerfile {
				problems = append(problems, fmt.Sprintf("image %q is published as suffix %q / dockerfile %q, want %q / %q", want.Image, entry.Suffix, entry.Dockerfile, want.Suffix, want.Dockerfile))
			}
		}
	}
	for image := range byImage {
		if _, ok := expected[image]; !ok {
			problems = append(problems, fmt.Sprintf("unexpected image %q: add it to publishedMatrix (and its documentation) deliberately", image))
		}
		if len(byImage[image]) > 1 {
			problems = append(problems, fmt.Sprintf("image %q appears %d times in the matrix", image, len(byImage[image])))
		}
	}
	sort.Strings(problems)
	return problems
}

func TestDockerPublishMatrix(t *testing.T) {
	root := moduleRoot(t)
	src, err := os.ReadFile(filepath.Join(root, ".github/workflows/docker-publish.yml"))
	if err != nil {
		t.Fatalf("read docker-publish workflow: %v", err)
	}
	dockerfileExists := func(rel string) bool {
		info, err := os.Stat(filepath.Join(root, rel))
		return err == nil && !info.IsDir()
	}
	entries, err := unmarshalMatrix(src)
	if err != nil {
		t.Fatalf("read the publication matrix: %v", err)
	}

	t.Run("the real workflow publishes every image once and includes bff", func(t *testing.T) {
		if got, want := len(entries), len(publishedMatrix); got != want {
			t.Errorf("matrix has %d images (%v), want %d", got, images(entries), want)
		}
		if problems := matrixProblems(entries, dockerfileExists); len(problems) > 0 {
			t.Fatalf("docker-publish matrix is not the reviewed set:\n  %s", strings.Join(problems, "\n  "))
		}
	})

	t.Run("order does not matter", func(t *testing.T) {
		reversed := cloneMatrix(entries)
		for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
			reversed[i], reversed[j] = reversed[j], reversed[i]
		}
		if problems := matrixProblems(reversed, dockerfileExists); len(problems) > 0 {
			t.Fatalf("a reordered matrix must still pass:\n  %s", strings.Join(problems, "\n  "))
		}
	})

	// Removing any image, not only the BFF, must be caught: that is what "a new product replaces an
	// existing one" looks like in the file.
	t.Run("removing any published image is caught", func(t *testing.T) {
		for _, want := range publishedMatrix {
			problems := matrixProblems(dropImage(entries, want.Image), dockerfileExists)
			requireProblem(t, problems, fmt.Sprintf("missing image %q", want.Image))
		}
	})

	t.Run("a missing BFF entry fails", func(t *testing.T) {
		problems := matrixProblems(dropImage(entries, "bff"), dockerfileExists)
		requireProblem(t, problems, "BFF publication regression: expected exactly one bff image, found 0")
		requireProblem(t, problems, `missing image "bff"`)
	})

	t.Run("a duplicated BFF entry fails", func(t *testing.T) {
		duplicated := append(cloneMatrix(entries), matrixEntry{Image: "bff", Suffix: "-bff", Dockerfile: "deploy/bff/Dockerfile"})
		problems := matrixProblems(duplicated, dockerfileExists)
		requireProblem(t, problems, "BFF publication regression: expected exactly one bff image, found 2")
	})

	t.Run("a BFF entry with another suffix or Dockerfile fails", func(t *testing.T) {
		for _, mutate := range []struct {
			name  string
			apply func(*matrixEntry)
			want  string
		}{
			{"suffix", func(e *matrixEntry) { e.Suffix = "-bff-web" }, "suffix"},
			{"dockerfile", func(e *matrixEntry) { e.Dockerfile = "deploy/bff-web/Dockerfile" }, "dockerfile"},
		} {
			changed := cloneMatrix(entries)
			for i := range changed {
				if changed[i].Image == "bff" {
					mutate.apply(&changed[i])
				}
			}
			problems := matrixProblems(changed, dockerfileExists)
			if !anyProblemContains(problems, `image "bff" is published as`) || !anyProblemContains(problems, mutate.want) {
				t.Fatalf("a bff entry with the wrong %s must fail, got:\n  %s", mutate.name, strings.Join(problems, "\n  "))
			}
		}
	})

	t.Run("a new image substituting an existing one fails", func(t *testing.T) {
		substituted := cloneMatrix(entries)
		for i := range substituted {
			if substituted[i].Image == "metro" {
				substituted[i] = matrixEntry{Image: "trivia", Suffix: "-trivia", Dockerfile: "deploy/trivia/Dockerfile"}
			}
		}
		problems := matrixProblems(substituted, func(rel string) bool { return true })
		requireProblem(t, problems, `missing image "metro"`)
		requireProblem(t, problems, `unexpected image "trivia"`)
	})

	t.Run("a reused publication target fails", func(t *testing.T) {
		reused := cloneMatrix(entries)
		for i := range reused {
			if reused[i].Image == "timeline-web" {
				reused[i].Suffix = "-timeline"
			}
		}
		problems := matrixProblems(reused, dockerfileExists)
		requireProblem(t, problems, `publication target "-timeline" is used by both "timeline" and "timeline-web"`)
	})

	t.Run("a Dockerfile that is not in the repository fails", func(t *testing.T) {
		changed := cloneMatrix(entries)
		for i := range changed {
			if changed[i].Image == "bff" {
				changed[i].Dockerfile = "deploy/ghost/Dockerfile"
			}
		}
		problems := matrixProblems(changed, dockerfileExists)
		requireProblem(t, problems, `image "bff" builds "deploy/ghost/Dockerfile", which is not in the repository`)
	})

	// The matrix is parsed as YAML structure, never searched as text: a `bff:` line in a comment or a
	// build argument is not an image.
	t.Run("bff only in a comment is not an entry", func(t *testing.T) {
		commented := []byte(`
jobs:
  build:
    strategy:
      matrix:
        include:
          # - image: bff
          #   suffix: '-bff'
          #   dockerfile: deploy/bff/Dockerfile
          - image: tripmap
            suffix: '-tripmap'
            dockerfile: deploy/tripmap/Dockerfile
            build_args: |
              BFF_SUFFIX=-bff
`)
		parsed, err := unmarshalMatrix(commented)
		if err != nil {
			t.Fatalf("parse the commented workflow: %v", err)
		}
		problems := matrixProblems(parsed, func(rel string) bool { return true })
		requireProblem(t, problems, "BFF publication regression: expected exactly one bff image, found 0")
	})

	t.Run("a workflow without a matrix fails to parse", func(t *testing.T) {
		if _, err := unmarshalMatrix([]byte("jobs:\n  build:\n    runs-on: ubuntu-latest\n")); err == nil {
			t.Fatal("a build job without strategy.matrix.include must be an error, not an empty pass")
		}
	})
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func cloneMatrix(entries []matrixEntry) []matrixEntry {
	return append([]matrixEntry(nil), entries...)
}

func dropImage(entries []matrixEntry, image string) []matrixEntry {
	kept := make([]matrixEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Image != image {
			kept = append(kept, entry)
		}
	}
	return kept
}

func images(entries []matrixEntry) []string {
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Image)
	}
	return names
}

func anyProblemContains(problems []string, want string) bool {
	for _, problem := range problems {
		if strings.Contains(problem, want) {
			return true
		}
	}
	return false
}

func requireProblem(t *testing.T, problems []string, want string) {
	t.Helper()
	if !anyProblemContains(problems, want) {
		t.Fatalf("expected a problem containing %q, got:\n  %s", want, strings.Join(problems, "\n  "))
	}
}

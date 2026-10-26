package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	bffconfig "github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/gateway"
	usersrvconfig "github.com/kongken/mono/internal/usersrv/config"
)

// TestRealMailStackConfigsAgree runs scripts/real-mail-stack.sh `generate` (no service, database or network is
// started) and loads what it wrote with the production validation of the BFF and of usersrv. Since T2 every
// registration and password-reset mail links to the central OzID, whichever product asked, so the configs the
// manual smoke stack runs on must agree on that one origin: usersrv's auth.apps.ozid.origin (where mail links
// are built), the BFF's navigation.apps.ozid.origin (where a sign-in is returned from) and an entry of the BFF's
// cors_allowed_origins; the products' origins are only the BFF's. Reserved loopback addresses only.
func TestRealMailStackConfigsAgree(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		env  []string
		ozid string
	}{
		{"defaults", nil, "http://localhost:5175"},
		{"overridden", []string{"OZID_WEB=http://localhost:4176", "TRIPMAP_WEB=http://localhost:4274", "METRO_WEB=http://localhost:4275"}, "http://localhost:4176"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command("bash", filepath.Join(root, "scripts", "real-mail-stack.sh"), "generate")
			cmd.Env = append(os.Environ(), append([]string{"REAL_STACK_DIR=" + dir}, tc.env...)...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("generate: %v\n%s", err, out)
			}

			var us usersrvconfig.AppConfig
			read(t, filepath.Join(dir, "config.usersrv.yaml"), &us)
			if err := us.Validate(); err != nil {
				t.Fatalf("usersrv config: %v", err)
			}
			var bff bffconfig.Config
			read(t, filepath.Join(dir, "config.bff.yaml"), &bff)
			active := gateway.Active(gateway.Services, bff.Upstreams)
			if err := bff.Validate(gateway.Names(active)); err != nil {
				t.Fatalf("bff config: %v", err)
			}

			mail := us.Auth.Apps["ozid"].Origin
			if mail != tc.ozid {
				t.Fatalf("mail links go to %q, want the central OzID %q", mail, tc.ozid)
			}
			if got := bff.Navigation.Apps["ozid"].Origin; got != mail {
				t.Fatalf("BFF navigation.apps.ozid.origin %q differs from usersrv auth.apps.ozid.origin %q", got, mail)
			}
			if !slices.Contains(bff.CORSAllowedOrigins, mail) {
				t.Fatalf("OzID %q is not in cors_allowed_origins %v", mail, bff.CORSAllowedOrigins)
			}
			// No new mail links to a product: usersrv only has the optional, commented transition entries.
			for id := range us.Auth.Apps {
				if id != "ozid" {
					t.Fatalf("usersrv links mail to %q too; only the central OzID is wanted", id)
				}
			}
			// Every product origin the BFF returns a sign-in to is a CORS origin of its own.
			for id, app := range bff.Navigation.Apps {
				if !slices.Contains(bff.CORSAllowedOrigins, app.Origin) && id != "admin" {
					t.Fatalf("navigation app %q origin %q is not in cors_allowed_origins", id, app.Origin)
				}
			}
		})
	}
}

func read(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

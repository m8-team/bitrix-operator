package runtime

import (
	"encoding/json"
	"strings"
	"testing"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
)

func TestSiteRouting(t *testing.T) {
	s := sample(t)
	s.Sites = []bx.BitrixSiteSpec{{ID: "s1", Hosts: []string{"one.example.com", "alias.example.com"}}, {ID: "s2", Hosts: []string{"two.example.com"}}}
	b, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range []string{`one.example.com "s1";`, `alias.example.com "s1";`, `two.example.com "s2";`, `fastcgi_param BITRIX_SITE_ID $operator_site_id;`} {
		if !strings.Contains(b.Nginx["nginx.conf"], pair) {
			t.Fatalf("missing host routing %s", pair)
		}
	}
	s.Sites[1].ID = `s2"; injected`
	if _, err = Render(s); err == nil {
		t.Fatal("unsafe site ID accepted")
	}
	s.Sites[1].ID = "s1"
	if _, err = Render(s); err == nil {
		t.Fatal("duplicate site ID accepted")
	}
}

func testSitesContainer(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "failure", "exit", "writable"} {
		t.Run(mode, func(t *testing.T) {
			s := sample(t)
			if mode == "missing" {
				s.Sites = append(s.Sites, bx.BitrixSiteSpec{ID: "absent", Hosts: []string{"absent.example.com"}})
			}
			b, err := Render(s)
			if err != nil {
				t.Fatal(err)
			}
			config := writeFiles(t, b.PHP)
			secretFiles := map[string]string{}
			for _, projection := range b.Secrets {
				for _, item := range projection.Secret.Items {
					secretFiles[item.Path] = "test-only"
				}
			}
			secrets := writeFiles(t, secretFiles)
			identity := ValidationIdentity{ClusterUID: "fixture", Generation: 1, Release: "sha256:" + strings.Repeat("a", 64), PHP: "sha256:" + strings.Repeat("b", 64), Attempt: "fixture-job"}
			data, _ := json.Marshal(identity)
			args := append(containerArgs(), "-v", config+":"+ConfigRoot+":ro", "-v", secrets+":"+SecretRoot+":ro", "-e", "BITRIX_VALIDATION_IDENTITY="+string(data), "-e", "BITRIX_VALIDATION_ATTEMPT=fixture-job", "-e", "FIXTURE_SITE_MODE="+mode,
				"--entrypoint=sh", "bitrix-runtime-fixture", "-ec", `php -d display_errors=0 -d log_errors=0 /run/bitrix-config/validate-sites.php >/tmp/stdout 2>/tmp/stderr || true; test ! -s /tmp/stdout; test ! -s /tmp/stderr; cat /tmp/site-result.json`)
			output := docker(t, args...)
			requested := make([]string, 0, len(s.Sites))
			for _, site := range s.Sites {
				requested = append(requested, site.ID)
			}
			result, err := ParseSiteResult([]byte(output), identity, requested)
			if err != nil {
				t.Fatalf("%s: %v", output, err)
			}
			want := "ValidationUnavailable"
			if mode == "valid" {
				want = "Validated"
			}
			if mode == "missing" {
				want = "SiteNotFound"
			}
			if result.Reason != want {
				t.Fatalf("got %s want %s", result.Reason, want)
			}
		})
	}
}

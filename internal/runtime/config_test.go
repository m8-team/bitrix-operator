package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// Goldens are reviewed expectations, updated explicitly by hack/update-golden, never by tests.
func TestNativeTemplateGolden(t *testing.T) {
	for _, name := range []string{"default", "custom"} {
		t.Run(name, func(t *testing.T) {
			spec := sample(t)
			if name == "custom" {
				spec.Sites[0].Hosts = []string{"z.example.com", "a.example.com"}
				spec.Storage.Uploads.MountPath = "/opt/www/media.v2+files"
				spec.Storage.Temporary.MountPath = "/opt/tmp-v2"
			}
			bundle, err := Render(spec)
			if err != nil {
				t.Fatal(err)
			}
			for component, files := range map[string]map[string]string{"nginx": bundle.Nginx, "php": bundle.PHP} {
				dir := filepath.Join("testdata", "golden", name, component)
				entries, readErr := os.ReadDir(dir)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if len(entries) != len(files) {
					t.Fatalf("%s output file set changed", component)
				}
				for _, entry := range entries {
					want, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
					if readErr != nil {
						t.Fatal(readErr)
					}
					got, exists := files[entry.Name()]
					if !exists || got != string(want) {
						t.Errorf("%s/%s differs from the reviewed golden", component, entry.Name())
					}
				}
			}
			hash, err := os.ReadFile(filepath.Join("testdata", "golden", name, "bundle.sha256"))
			if err != nil {
				t.Fatal(err)
			}
			if bundle.Hash+"\n" != string(hash) {
				t.Fatalf("runtime bundle differs from the reviewed rollout hash: got %s, want %s", bundle.Hash, strings.TrimSpace(string(hash)))
			}
			if name == "custom" {
				spec.Sites[0].Hosts[0], spec.Sites[0].Hosts[1] = spec.Sites[0].Hosts[1], spec.Sites[0].Hosts[0]
				reordered, renderErr := Render(spec)
				if renderErr != nil {
					t.Fatal(renderErr)
				}
				if reordered.Hash != bundle.Hash {
					t.Fatal("host order changed the rollout hash")
				}
			}
		})
	}
}

func TestTemplateFailures(t *testing.T) {
	valid := configData{Hosts: []string{"portal.example.com"}, UploadsPath: Root + "/upload", TemporaryPath: "/opt/.bx_temp"}
	cases := []struct {
		name, source string
		data         any
	}{
		{"missing file", "templates/nginx/missing.conf.tmpl", valid},
		{"missing host field", "templates/nginx/nginx.conf.tmpl", map[string]any{"UploadsPath": Root + "/upload"}},
		{"missing path field", "templates/php/php.ini.tmpl", map[string]any{}},
		{"empty hosts", "templates/nginx/nginx.conf.tmpl", configData{UploadsPath: valid.UploadsPath}},
		{"host injection", "templates/nginx/nginx.conf.tmpl", configData{Hosts: []string{"x; return 200;"}, UploadsPath: valid.UploadsPath}},
		{"empty uploads", "templates/nginx/nginx.conf.tmpl", configData{Hosts: valid.Hosts}},
		{"uploads injection", "templates/nginx/nginx.conf.tmpl", configData{Hosts: valid.Hosts, UploadsPath: Root + "/upload; deny all;"}},
		{"INI injection", "templates/php/php.ini.tmpl", configData{TemporaryPath: "/opt/tmp\"\ndisplay_errors=On"}},
		{"empty temporary path", "templates/php/php.ini.tmpl", configData{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderFile(configFiles, tc.source, tc.data)
			if err == nil || got != "" {
				t.Fatal("invalid template input returned a partial or successful config")
			}
		})
	}
	malformed := fstest.MapFS{"broken.conf.tmpl": {Data: []byte("prefix {{")}}
	if got, err := renderFile(malformed, "broken.conf.tmpl", valid); err == nil || got != "" {
		t.Fatal("malformed template accepted")
	}
}

func TestStaticPHPIsNotTemplated(t *testing.T) {
	literal := "<?php $uri = '{{ .NotAField }}'; return $uri;\n"
	files := fstest.MapFS{"settings.php": {Data: []byte(literal)}}
	got, err := renderFile(files, "settings.php", nil)
	if err != nil || got != literal {
		t.Fatal("native PHP content was interpreted as a template")
	}
}

func TestPrepareReleaseUsesNativeFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "bitrix"), 0755); err != nil {
		t.Fatal(err)
	}
	originals := map[string][]byte{}
	for _, name := range []string{".settings.php", ".settings_extra.php"} {
		data, err := os.ReadFile(filepath.Join("../../test/fixtures/runtime/bitrix", name))
		if err != nil {
			t.Fatal(err)
		}
		originals[name] = data
		if err = os.WriteFile(filepath.Join(root, "bitrix", name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	run := func() error {
		return exec.CommandContext(t.Context(), "sh", "../../hack/runtime/prepare-release.sh", root, "templates/bitrix").Run()
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	for name, want := range originals {
		saved := strings.TrimSuffix(name, ".php") + ".release.php"
		got, err := os.ReadFile(filepath.Join(root, "bitrix", saved))
		if err != nil || string(got) != string(want) {
			t.Fatalf("release source %s was not preserved", name)
		}
	}
	for target, source := range map[string]string{".settings.php": "settings-wrapper.php", ".settings_extra.php": "settings-extra.php", "license_key.php": "license-placeholder.php"} {
		got, err := os.ReadFile(filepath.Join(root, "bitrix", target))
		if err != nil {
			t.Fatal(err)
		}
		want, err := configFiles.ReadFile("templates/bitrix/" + source)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s differs from the native build template", target)
		}
	}
	if err := run(); err == nil {
		t.Fatal("preparation unexpectedly overwrote existing release originals")
	}
}

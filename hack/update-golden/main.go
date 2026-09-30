// Command update-golden explicitly refreshes reviewed runtime expectations.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	"sigs.k8s.io/yaml"
)

func main() {
	if err := update(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func update() error {
	data, err := os.ReadFile("config/samples/bitrix_v1alpha1_bitrixcluster.yaml")
	if err != nil {
		return err
	}
	for _, name := range []string{"default", "custom"} {
		var c bx.BitrixCluster
		if err = yaml.Unmarshal(data, &c); err != nil {
			return err
		}
		if name == "custom" {
			c.Spec.Sites[0].Hosts = []string{"z.example.com", "a.example.com"}
			c.Spec.Storage.Uploads.MountPath = "/opt/www/media.v2+files"
			c.Spec.Storage.Temporary.MountPath = "/opt/tmp-v2"
		}
		b, renderErr := render.Render(c.Spec)
		if renderErr != nil {
			return renderErr
		}
		root := filepath.Join("internal/runtime/testdata/golden", name)
		for component, files := range map[string]map[string]string{"nginx": b.Nginx, "php": b.PHP} {
			for file, content := range files {
				if err = os.WriteFile(filepath.Join(root, component, file), []byte(content), 0644); err != nil {
					return err
				}
			}
		}
		if err = os.WriteFile(filepath.Join(root, "bundle.sha256"), []byte(b.Hash+"\n"), 0644); err != nil {
			return err
		}
	}
	return nil
}

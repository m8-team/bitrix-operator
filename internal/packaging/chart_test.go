package packaging

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

func root(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root(t), path))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestChartSources(t *testing.T) {
	for source, target := range map[string]string{
		"config/crd/bases/bitrix.m8.team_bitrixclusters.yaml": "dist/chart/crds/bitrix.m8.team_bitrixclusters.yaml",
		"config/rbac/role.yaml":                               "dist/chart/files/manager-role.yaml",
	} {
		if !bytes.Equal(read(t, source), read(t, target)) {
			t.Fatalf("%s differs from %s; run make sync-chart", target, source)
		}
	}
}

func helm(t *testing.T, flags ...string) ([]byte, error) {
	t.Helper()
	if os.Getenv("BITRIX_CHART_TESTS") != "1" {
		t.Skip("set BITRIX_CHART_TESTS=1 to require Helm rendering tests")
	}
	binary := filepath.Join(root(t), "bin/helm")
	args := make([]string, 0, 8+len(flags))
	args = append(args, "template", "preview", "dist/chart", "--namespace", "operator-test", "--kube-version", "1.37.0", "--include-crds")
	command := exec.Command(binary, append(args, flags...)...)
	command.Dir = root(t)
	return command.CombinedOutput()
}

func render(t *testing.T, flags ...string) []*unstructured.Unstructured {
	t.Helper()
	out, err := helm(t, flags...)
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(out), 4096)
	var objects []*unstructured.Unstructured
	for {
		u := &unstructured.Unstructured{}
		if err := decoder.Decode(u); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if len(u.Object) != 0 {
			objects = append(objects, u)
		}
	}
	return objects
}

func TestChartRuntimeAndRBAC(t *testing.T) {
	image := "registry.example.test:5443/operator@sha256:" + strings.Repeat("a", 64)
	objects := render(t, "--set-string", "manager.image.reference="+image)
	var deployment appsv1.Deployment
	var service corev1.Service
	var role rbacv1.ClusterRole
	for _, obj := range objects {
		var target any
		switch {
		case obj.GetKind() == "Deployment":
			target = &deployment
		case obj.GetKind() == "Service":
			target = &service
		case obj.GetKind() == "ClusterRole" && strings.HasSuffix(obj.GetName(), "-manager-role"):
			target = &role
		}
		if target != nil {
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, target); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(deployment.Spec.Template.Spec.Containers) != 1 {
		t.Fatal("expected one manager container")
	}
	manager := deployment.Spec.Template.Spec.Containers[0]
	if manager.Image != image || manager.SecurityContext.ReadOnlyRootFilesystem == nil || !*manager.SecurityContext.ReadOnlyRootFilesystem {
		t.Fatal("image reference or read-only filesystem was lost")
	}
	if deployment.Spec.Template.Spec.SecurityContext.RunAsNonRoot == nil || !*deployment.Spec.Template.Spec.SecurityContext.RunAsNonRoot {
		t.Fatal("manager must run as non-root")
	}
	if !reflect.DeepEqual(service.Spec.Selector, deployment.Spec.Selector.MatchLabels) || service.Spec.Selector["app.kubernetes.io/instance"] != "preview" {
		t.Fatal("metrics must select only this release's manager")
	}
	if service.Spec.Ports[0].Name != "https" || service.Spec.Ports[0].Port != 8443 {
		t.Fatal("secure metrics defaults were lost")
	}
	var expected rbacv1.ClusterRole
	if err := yaml.Unmarshal(read(t, "config/rbac/role.yaml"), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(role.Rules, expected.Rules) {
		t.Fatal("chart manager RBAC differs from generated RBAC")
	}
}

func TestChartOptions(t *testing.T) {
	objects := render(t, "--set-string", "manager.image.reference=registry.example.test:5443/operator:dev", "--set", "metrics.enabled=false", "--set", "serviceAccount.enabled=false", "--set-string", "serviceAccount.name=external", "--set", "manager.healthProbe.port=9091", "--kube-version", "1.36.0")
	for _, obj := range objects {
		if obj.GetKind() == "Service" || obj.GetKind() == "ServiceAccount" {
			t.Fatalf("disabled resource rendered: %s", obj.GetKind())
		}
		if obj.GetKind() == "Deployment" {
			var d appsv1.Deployment
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &d); err != nil {
				t.Fatal(err)
			}
			if d.Spec.Template.Spec.ServiceAccountName != "external" || d.Spec.Template.Spec.Containers[0].ReadinessProbe.HTTPGet.Port.IntVal != 9091 {
				t.Fatal("custom service account or health port was lost")
			}
		}
	}
}

func TestChartRejectsUnsupportedSettings(t *testing.T) {
	for _, flags := range [][]string{
		nil,
		{"--set", "rbac.namespaced=true"},
		{"--set", "webhook.enabled=true"},
		{"--set", "manager.replicas=0"},
		{"--set", "serviceAccount.enabled=false"},
		{"--kube-version", "1.35.0"},
	} {
		args := flags
		if flags != nil {
			args = append([]string{"--set-string", "manager.image.reference=controller:test"}, flags...)
		}
		if output, err := helm(t, args...); err == nil {
			t.Fatalf("unsupported values accepted: %v\n%s", flags, output)
		}
	}
}

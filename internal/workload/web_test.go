package workload

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"
)

func fixture(t *testing.T) *bx.BitrixCluster {
	t.Helper()
	data, err := os.ReadFile("../../config/samples/bitrix_v1alpha1_bitrixcluster.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c := &bx.BitrixCluster{}
	if err = yaml.Unmarshal(data, c); err != nil {
		t.Fatal(err)
	}
	c.UID = types.UID("test-uid")
	c.Namespace = "test"
	c.Spec.Runtime.Web.Replicas = 1
	return c
}

func TestPodIsolation(t *testing.T) {
	c := fixture(t)
	b, err := render.Render(c.Spec)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(Deployment(c, b, "revision", "release@sha256:"+strings.Repeat("a", 64)))
	if err != nil {
		t.Fatal(err)
	}
	d := &appsv1.Deployment{}
	if err = json.Unmarshal(data, d); err != nil {
		t.Fatal(err)
	}
	pod := d.Spec.Template.Spec
	assertWebLifecycle(t, c, b, pod)
	if *pod.AutomountServiceAccountToken || *pod.SecurityContext.FSGroup != 979 || pod.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatal("Pod isolation regressed")
	}
	for _, container := range append(pod.InitContainers, pod.Containers...) {
		if !*container.SecurityContext.ReadOnlyRootFilesystem || *container.SecurityContext.AllowPrivilegeEscalation || *container.SecurityContext.RunAsUser == 0 {
			t.Fatalf("insecure container %s", container.Name)
		}
		for _, m := range container.VolumeMounts {
			if m.Name == "credentials" && container.Name != "php" {
				t.Fatal("credentials leaked to a non-PHP container")
			}
			if m.MountPath == render.Root && !m.ReadOnly {
				t.Fatal("runtime code is writable")
			}
			if container.Name == "nginx" && m.Name == "uploads" && !m.ReadOnly {
				t.Fatal("nginx uploads are writable")
			}
		}
	}
	if len(pod.InitContainers) != 1 || len(pod.Containers) != 2 {
		t.Fatal("unexpected workload topology")
	}
	if pod.Containers[0].ReadinessProbe.HTTPGet.Port.IntVal != 8081 || pod.Containers[0].LivenessProbe.HTTPGet.Path != "/live" {
		t.Fatal("dependency readiness and liveness must be separate")
	}
	if d.Spec.Strategy.Type != appsv1.RollingUpdateDeploymentStrategyType {
		t.Fatal("RWX should support rolling updates")
	}
	c.Spec.Storage.Uploads.PersistentVolumeClaim.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	if *Deployment(c, b, "revision", "release").Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Fatal("RWO must avoid overlapping Pods on rollout")
	}
}

func TestNamesAreDNSLabelsAndCollisionResistant(t *testing.T) {
	names := map[string]bool{}
	for _, original := range []string{"simple", "portal.example.com", "portal-example-com", strings.Repeat("a", 253), strings.Repeat("a", 252) + "b"} {
		c := fixture(t)
		c.Name = original
		name := Name(c, "nginx-123456789abc")
		if problems := validation.IsDNS1123Label(name); len(problems) != 0 {
			t.Fatalf("invalid resource name: %v", problems)
		}
		if names[name] {
			t.Fatal("distinct names collide")
		}
		names[name] = true
	}
}

func assertWebLifecycle(t *testing.T, c *bx.BitrixCluster, b *render.Bundle, pod corev1.PodSpec) {
	t.Helper()
	if *pod.TerminationGracePeriodSeconds != 360 || len(pod.TopologySpreadConstraints) != 1 ||
		pod.TopologySpreadConstraints[0].WhenUnsatisfiable != corev1.ScheduleAnyway ||
		pod.TopologySpreadConstraints[0].LabelSelector.MatchLabels[OwnerUID] != string(c.UID) {
		t.Fatal("web shutdown budget or cluster-scoped spreading missing")
	}
	for _, container := range pod.Containers {
		if container.Lifecycle == nil || container.Lifecycle.PreStop == nil || container.Lifecycle.PreStop.Exec == nil {
			t.Fatalf("missing graceful shutdown hook: %s", container.Name)
		}
	}
	cronPod := Cron(c, b, "revision", "release").Spec.Template.Spec
	if *cronPod.TerminationGracePeriodSeconds != 30 || cronPod.Containers[0].Lifecycle != nil || len(cronPod.TopologySpreadConstraints) != 0 {
		t.Fatal("web termination policy leaked into cron")
	}
}

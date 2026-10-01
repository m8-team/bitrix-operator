package workload

import (
	"encoding/json"
	"testing"

	render "github.com/m8-team/bitrix-operator/internal/runtime"
	appsv1 "k8s.io/api/apps/v1"
)

func TestCronPodIsolation(t *testing.T) {
	c := fixture(t)
	b, err := render.Render(c.Spec)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(Cron(c, b, "revision", "release:test"))
	if err != nil {
		t.Fatal(err)
	}
	d := &appsv1.Deployment{}
	if err = json.Unmarshal(data, d); err != nil {
		t.Fatal(err)
	}
	pod := d.Spec.Template.Spec
	if len(pod.Containers) != 1 || len(pod.InitContainers) != 1 || *pod.AutomountServiceAccountToken {
		t.Fatal("invalid cron topology")
	}
	cron := pod.Containers[0]
	if cron.Image != c.Spec.Runtime.Web.PHP.Image || cron.Command[1] != render.ConfigRoot+"/scheduler.php" {
		t.Fatal("cron command/image mismatch")
	}
	if !*cron.SecurityContext.ReadOnlyRootFilesystem || *cron.SecurityContext.AllowPrivilegeEscalation || *cron.SecurityContext.RunAsUser != 979 {
		t.Fatal("cron isolation regressed")
	}
	for _, v := range pod.Volumes {
		if v.Name == "nginx-config" || v.Name == "nginx-tmp" {
			t.Fatal("cron has nginx volumes")
		}
	}
	for _, m := range pod.InitContainers[0].VolumeMounts {
		if m.Name == "credentials" {
			t.Fatal("release init has credentials")
		}
	}
	if cron.LivenessProbe.Exec.Command[2] != "live" || cron.ReadinessProbe.Exec.Command[2] != "ready" {
		t.Fatal("health probes confused")
	}
	if *d.Spec.Replicas != 1 || d.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Fatal("singleton strategy regressed")
	}
}

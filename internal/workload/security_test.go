package workload

import (
	"encoding/json"
	"slices"
	"testing"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	corev1 "k8s.io/api/core/v1"
	core "k8s.io/client-go/applyconfigurations/core/v1"
)

func TestAllWorkloadSecurityContexts(t *testing.T) {
	c := fixture(t)
	c.Spec.Runtime.Push = &bx.PushRuntimeSpec{Enabled: true, Image: "push:test",
		SecretRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "push-key"}, Key: "key"},
		Publisher: bx.ReplicaSpec{Replicas: 1}, Subscriber: bx.ReplicaSpec{Replicas: 1}}
	b, err := render.Render(c.Spec, PushEndpoints(c))
	if err != nil {
		t.Fatal(err)
	}
	pods := map[string]*core.PodTemplateSpecApplyConfiguration{
		"bitrix":                 Deployment(c, b, "revision", "release").Spec.Template,
		"bitrix-cron":            Cron(c, b, "revision", "release").Spec.Template,
		"bitrix-sites":           SiteValidation(c, b, "revision", "release", "release-digest", "php-digest").Spec.Template,
		"bitrix-push-publisher":  PushDeployment(c, b, "revision", "publisher").Spec.Template,
		"bitrix-push-subscriber": PushDeployment(c, b, "revision", "subscriber").Spec.Template,
	}
	for name, template := range pods {
		t.Run(name, func(t *testing.T) {
			if template.Labels[appNameLabel] != name || template.Labels[OwnerUID] != string(c.UID) {
				t.Fatal("Pod labels no longer match network policy contract")
			}
			data, err := json.Marshal(template.Spec)
			if err != nil {
				t.Fatal(err)
			}
			var pod corev1.PodSpec
			if err := json.Unmarshal(data, &pod); err != nil {
				t.Fatal(err)
			}
			assertPodSecurity(t, pod)
		})
	}
}

func assertPodSecurity(t *testing.T, pod corev1.PodSpec) {
	t.Helper()
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken || pod.HostNetwork || pod.HostPID || pod.HostIPC {
		t.Fatal("workload acquired API token or host namespace access")
	}
	if pod.SecurityContext == nil || pod.SecurityContext.SeccompProfile == nil || pod.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatal("missing RuntimeDefault seccomp")
	}
	for _, container := range append(pod.InitContainers, pod.Containers...) {
		assertContainerSecurity(t, container)
	}
	for _, volume := range pod.Volumes {
		if volume.HostPath != nil {
			t.Fatal("hostPath is forbidden")
		}
		if volume.Projected != nil {
			for _, source := range volume.Projected.Sources {
				if source.ServiceAccountToken != nil {
					t.Fatal("explicit API token projection bypasses automount=false")
				}
			}
		}
	}
}

func assertContainerSecurity(t *testing.T, container corev1.Container) {
	t.Helper()
	s := container.SecurityContext
	if s == nil || s.RunAsNonRoot == nil || !*s.RunAsNonRoot || s.RunAsUser == nil || *s.RunAsUser <= 0 ||
		s.AllowPrivilegeEscalation == nil || *s.AllowPrivilegeEscalation || s.ReadOnlyRootFilesystem == nil || !*s.ReadOnlyRootFilesystem ||
		(s.Privileged != nil && *s.Privileged) || s.Capabilities == nil || !slices.Contains(s.Capabilities.Drop, "ALL") || len(s.Capabilities.Add) != 0 {
		t.Fatalf("unsafe container %s", container.Name)
	}
	if s.SeccompProfile != nil && s.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("container %s overrides Pod seccomp", container.Name)
	}
	for _, port := range container.Ports {
		if port.HostPort != 0 {
			t.Fatal("hostPort bypasses workload isolation")
		}
	}
}

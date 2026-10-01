package workload

import (
	"encoding/json"
	"testing"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
)

func TestPushIsolation(t *testing.T) {
	c := fixture(t)
	c.Spec.Runtime.Push = &bx.PushRuntimeSpec{Enabled: true, Image: "push:test", SecretRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "push-key"}, Key: "key"}, Publisher: bx.ReplicaSpec{Replicas: 1}, Subscriber: bx.ReplicaSpec{Replicas: 2}}
	b, err := render.Render(c.Spec, PushEndpoints(c))
	if err != nil {
		t.Fatal(err)
	}
	decode := func(source, target any) {
		t.Helper()
		data, e := json.Marshal(source)
		if e != nil {
			t.Fatal(e)
		}
		if e = json.Unmarshal(data, target); e != nil {
			t.Fatal(e)
		}
	}
	for _, role := range []string{"publisher", "subscriber"} {
		d := &appsv1.Deployment{}
		decode(PushDeployment(c, b, "revision", role), d)
		pod := d.Spec.Template.Spec
		if len(pod.Containers) != 1 || len(pod.InitContainers) != 0 || *pod.AutomountServiceAccountToken {
			t.Fatal("unexpected Push topology")
		}
		container := pod.Containers[0]
		if !*container.SecurityContext.ReadOnlyRootFilesystem || *container.SecurityContext.AllowPrivilegeEscalation || *pod.SecurityContext.RunAsUser != 979 {
			t.Fatal("Push security context regressed")
		}
		checkPushVolumes(t, c, pod.Volumes)
		if container.LivenessProbe.Exec.Command[2] != "live" || container.ReadinessProbe.Exec.Command[2] != "ready" {
			t.Fatal("probes confused")
		}
		policy := &networkingv1.NetworkPolicy{}
		decode(PushPolicy(c, role), policy)
		if len(policy.Spec.Ingress) != 1 || len(policy.Spec.PolicyTypes) != 1 || policy.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
			t.Fatal("unexpected network isolation")
		}
		for _, peer := range policy.Spec.Ingress[0].From {
			if peer.NamespaceSelector != nil || peer.IPBlock != nil || peer.PodSelector.MatchLabels[OwnerUID] != string(c.UID) {
				t.Fatal("ingress crosses cluster/namespace boundary")
			}
			app := peer.PodSelector.MatchLabels["app.kubernetes.io/name"]
			if app != "bitrix" && (role != "publisher" || app != "bitrix-cron") {
				t.Fatal("unexpected ingress peer")
			}
		}
		service := &corev1.Service{}
		decode(PushService(c, role), service)
		if service.Spec.Type != corev1.ServiceTypeClusterIP || service.Spec.Selector["app.kubernetes.io/name"] != d.Spec.Template.Labels["app.kubernetes.io/name"] || service.Spec.Selector["app.kubernetes.io/name"] == labels(c)["app.kubernetes.io/name"] {
			t.Fatal("Push Service exposure/selector regressed")
		}
	}
}

func checkPushVolumes(t *testing.T, c *bx.BitrixCluster, volumes []corev1.Volume) {
	t.Helper()
	for _, v := range volumes {
		if v.PersistentVolumeClaim != nil {
			t.Fatal("Push has application storage")
		}
		if v.Projected != nil {
			for _, p := range v.Projected.Sources {
				if p.Secret == nil || (p.Secret.Name != "push-key" && p.Secret.Name != c.Spec.Redis.PasswordSecretRef.Name) {
					t.Fatal("Push has application credentials")
				}
			}
		}
		if v.EmptyDir != nil && v.EmptyDir.Medium != corev1.StorageMediumMemory {
			t.Fatal("Secret-derived config must remain on tmpfs")
		}
	}
}

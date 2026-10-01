package workload

import (
	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	apps "k8s.io/client-go/applyconfigurations/apps/v1"
	core "k8s.io/client-go/applyconfigurations/core/v1"
	meta "k8s.io/client-go/applyconfigurations/meta/v1"
	networking "k8s.io/client-go/applyconfigurations/networking/v1"
)

const pushPublisher = "publisher"
const appNameLabel = "app.kubernetes.io/name"

func PushEndpoints(c *bx.BitrixCluster) render.PushEndpoints {
	return render.PushEndpoints{Publisher: Name(c, "push-publisher"), Subscriber: Name(c, "push-subscriber")}
}
func pushLabels(c *bx.BitrixCluster, role string) map[string]string {
	return map[string]string{appNameLabel: "bitrix-push-" + role, OwnerUID: string(c.UID)}
}
func pushPort(role string) int32 {
	if role == pushPublisher {
		return 9010
	}
	return 8010
}
func PushConfig(c *bx.BitrixCluster, b *render.Bundle) *core.ConfigMapApplyConfiguration {
	return core.ConfigMap(Name(c, "push-"+b.Hash[:12]), c.Namespace).WithLabels(labels(c)).WithOwnerReferences(owner(c)).WithImmutable(true).WithData(b.Push)
}
func PushService(c *bx.BitrixCluster, role string) *core.ServiceApplyConfiguration {
	return core.Service(Name(c, "push-"+role), c.Namespace).WithLabels(pushLabels(c, role)).WithOwnerReferences(owner(c)).WithSpec(
		core.ServiceSpec().WithType(corev1.ServiceTypeClusterIP).WithSelector(pushLabels(c, role)).WithPorts(core.ServicePort().WithName("http").WithPort(pushPort(role)).WithTargetPort(intstr.FromString("http"))))
}

// No release code or database/license/crypto credentials enter Push Pods.
func PushDeployment(c *bx.BitrixCluster, b *render.Bundle, revision, role string) *apps.DeploymentApplyConfiguration {
	replicas := c.Spec.Runtime.Push.Subscriber.Replicas
	if role == pushPublisher {
		replicas = c.Spec.Runtime.Push.Publisher.Replicas
	}
	projected := core.ProjectedVolumeSource().WithDefaultMode(0440)
	for _, p := range b.PushSecrets {
		secret := core.SecretProjection().WithName(p.Secret.Name).WithOptional(false)
		for _, item := range p.Secret.Items {
			secret.WithItems(core.KeyToPath().WithKey(item.Key).WithPath(item.Path).WithMode(*item.Mode))
		}
		projected.WithSources(core.VolumeProjection().WithSecret(secret))
	}
	probe := func(mode string) *core.ProbeApplyConfiguration {
		return core.Probe().WithExec(core.ExecAction().WithCommand("node", "/run/push-config/health.cjs", mode)).WithTimeoutSeconds(3).WithPeriodSeconds(5).WithFailureThreshold(3)
	}
	pod := core.PodSpec().WithAutomountServiceAccountToken(false).WithTerminationGracePeriodSeconds(30).
		WithSecurityContext(core.PodSecurityContext().WithRunAsNonRoot(true).WithRunAsUser(979).WithRunAsGroup(979).WithFSGroup(979).WithSeccompProfile(core.SeccompProfile().WithType(corev1.SeccompProfileTypeRuntimeDefault))).
		WithVolumes(core.Volume().WithName("config").WithConfigMap(core.ConfigMapVolumeSource().WithName(Name(c, "push-"+b.Hash[:12])).WithDefaultMode(0444)),
			core.Volume().WithName("credentials").WithProjected(projected), core.Volume().WithName("tmp").WithEmptyDir(core.EmptyDirVolumeSource().WithMedium(corev1.StorageMediumMemory).WithSizeLimit(resource.MustParse("128Mi")))).
		WithContainers(core.Container().WithName(role).WithImage(c.Spec.Runtime.Push.Image).WithImagePullPolicy(corev1.PullIfNotPresent).WithSecurityContext(security()).
			WithCommand("/bin/sh", "/run/push-config/start.sh", role).WithPorts(core.ContainerPort().WithName("http").WithContainerPort(pushPort(role))).
			WithVolumeMounts(mount("config", "/run/push-config", true), mount("credentials", render.SecretRoot, true), mount("tmp", "/tmp", false)).
			WithStartupProbe(probe("live").WithFailureThreshold(30)).WithLivenessProbe(probe("live")).WithReadinessProbe(probe("ready")))
	for _, ref := range c.Spec.Release.ImagePullSecrets {
		pod.WithImagePullSecrets(core.LocalObjectReference().WithName(ref.Name))
	}
	return apps.Deployment(Name(c, "push-"+role), c.Namespace).WithLabels(pushLabels(c, role)).WithOwnerReferences(owner(c)).WithSpec(
		apps.DeploymentSpec().WithReplicas(replicas).WithRevisionHistoryLimit(2).WithProgressDeadlineSeconds(600).
			WithStrategy(apps.DeploymentStrategy().WithType("RollingUpdate").WithRollingUpdate(apps.RollingUpdateDeployment().WithMaxSurge(intstr.FromInt32(1)).WithMaxUnavailable(intstr.FromInt32(0)))).
			WithSelector(meta.LabelSelector().WithMatchLabels(pushLabels(c, role))).WithTemplate(core.PodTemplateSpec().WithLabels(pushLabels(c, role)).WithAnnotations(map[string]string{ConfigHash: b.Hash, SecretRevision: revision}).WithSpec(pod)))
}

// The trusted publication API is reachable only by this cluster's web and cron Pods.
// An enforcing CNI is required; Kubernetes accepting a policy is not proof of enforcement.
func PushPolicy(c *bx.BitrixCluster, role string) *networking.NetworkPolicyApplyConfiguration {
	peers := []*networking.NetworkPolicyPeerApplyConfiguration{networking.NetworkPolicyPeer().WithPodSelector(meta.LabelSelector().WithMatchLabels(labels(c)))}
	if role == pushPublisher {
		peers = append(peers, networking.NetworkPolicyPeer().WithPodSelector(meta.LabelSelector().WithMatchLabels(map[string]string{appNameLabel: "bitrix-cron", OwnerUID: string(c.UID)})))
	}
	return networking.NetworkPolicy(Name(c, "push-"+role), c.Namespace).WithLabels(pushLabels(c, role)).WithOwnerReferences(owner(c)).WithSpec(
		networking.NetworkPolicySpec().WithPodSelector(meta.LabelSelector().WithMatchLabels(pushLabels(c, role))).WithPolicyTypes(networkingv1.PolicyTypeIngress).
			WithIngress(networking.NetworkPolicyIngressRule().WithFrom(peers...).WithPorts(networking.NetworkPolicyPort().WithProtocol(corev1.ProtocolTCP).WithPort(intstr.FromInt32(pushPort(role))))))
}

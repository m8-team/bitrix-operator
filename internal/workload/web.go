// Package workload builds explicitly owned Kubernetes apply configurations.
package workload

import (
	"crypto/sha256"
	"fmt"
	"strings"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	apps "k8s.io/client-go/applyconfigurations/apps/v1"
	core "k8s.io/client-go/applyconfigurations/core/v1"
	meta "k8s.io/client-go/applyconfigurations/meta/v1"
)

const (
	OwnerUID       = "bitrix.m8.team/owner-uid"
	ClusterName    = "bitrix.m8.team/cluster-name"
	ConfigHash     = "bitrix.m8.team/config-hash"
	SecretRevision = "bitrix.m8.team/secret-revision"
)

// Name keeps deterministic resource names within the Service DNS label limit.
func Name(c *bx.BitrixCluster, suffix string) string {
	base := strings.ReplaceAll(c.Name, ".", "-")
	if len(base) > 30 || base != c.Name {
		if len(base) > 21 {
			base = base[:21]
		}
		base = strings.TrimRight(base, "-") + fmt.Sprintf("-%x", sha256.Sum256([]byte(c.Name)))[:9]
	}
	if suffix != "" {
		return base + "-" + suffix
	}
	return base
}
func labels(c *bx.BitrixCluster) map[string]string {
	return map[string]string{appNameLabel: "bitrix", OwnerUID: string(c.UID)}
}
func owner(c *bx.BitrixCluster) *meta.OwnerReferenceApplyConfiguration {
	return meta.OwnerReference().WithAPIVersion(bx.GroupVersion.String()).WithKind("BitrixCluster").WithName(c.Name).WithUID(c.UID).WithController(true)
}

// ConfigMaps are immutable and versioned; old Pods retain their exact configuration.
func ConfigMaps(c *bx.BitrixCluster, b *render.Bundle) []*core.ConfigMapApplyConfiguration {
	return []*core.ConfigMapApplyConfiguration{
		core.ConfigMap(Name(c, "nginx-"+b.Hash[:12]), c.Namespace).WithLabels(labels(c)).WithOwnerReferences(owner(c)).WithImmutable(true).WithData(b.Nginx),
		core.ConfigMap(Name(c, "php-"+b.Hash[:12]), c.Namespace).WithLabels(labels(c)).WithOwnerReferences(owner(c)).WithImmutable(true).WithData(b.PHP),
	}
}

func Service(c *bx.BitrixCluster) *core.ServiceApplyConfiguration {
	return core.Service(Name(c, "web"), c.Namespace).WithLabels(labels(c)).WithOwnerReferences(owner(c)).WithSpec(
		core.ServiceSpec().WithType(corev1.ServiceTypeClusterIP).WithSelector(labels(c)).WithPorts(
			core.ServicePort().WithName("http").WithPort(80).WithProtocol(corev1.ProtocolTCP).WithTargetPort(intstr.FromString("http"))))
}

// PVC is intentionally NOT an owned object: deleting a cluster must not delete user uploads.
func PVC(c *bx.BitrixCluster) *corev1.PersistentVolumeClaim {
	s := c.Spec.Storage.Uploads.PersistentVolumeClaim
	mode := corev1.PersistentVolumeFilesystem
	return &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: Name(c, "uploads"), Namespace: c.Namespace,
		Labels: labels(c), Annotations: map[string]string{ClusterName: c.Name}}, Spec: corev1.PersistentVolumeClaimSpec{
		StorageClassName: &s.StorageClassName, AccessModes: s.AccessModes, VolumeMode: &mode,
		Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: s.Size}}}}
}

func security() *core.SecurityContextApplyConfiguration {
	return core.SecurityContext().WithRunAsNonRoot(true).WithRunAsUser(979).WithRunAsGroup(979).
		WithAllowPrivilegeEscalation(false).WithReadOnlyRootFilesystem(true).
		WithCapabilities(core.Capabilities().WithDrop("ALL"))
}
func mount(name, path string, ro bool) *core.VolumeMountApplyConfiguration {
	return core.VolumeMount().WithName(name).WithMountPath(path).WithReadOnly(ro)
}
func resources(r corev1.ResourceRequirements) *core.ResourceRequirementsApplyConfiguration {
	return core.ResourceRequirements().WithRequests(r.Requests).WithLimits(r.Limits)
}
func httpProbe(path string) *core.ProbeApplyConfiguration {
	return core.Probe().WithHTTPGet(core.HTTPGetAction().WithPath(path).WithPort(intstr.FromInt32(8081))).
		WithTimeoutSeconds(8).WithPeriodSeconds(10).WithFailureThreshold(3)
}
func phpProbe() *core.ProbeApplyConfiguration {
	return core.Probe().WithExec(core.ExecAction().WithCommand("php", render.ConfigRoot+"/fpm-live.php")).
		WithTimeoutSeconds(3).WithPeriodSeconds(10).WithFailureThreshold(3)
}

// phpPod shares the release, storage and isolation contract between web and cron.
func phpPod(c *bx.BitrixCluster, b *render.Bundle, image string) *core.PodSpecApplyConfiguration {
	phpConfig := Name(c, "php-"+b.Hash[:12])
	mode := int32(0444)
	projected := core.ProjectedVolumeSource().WithDefaultMode(0440)
	for _, p := range b.Secrets {
		secret := core.SecretProjection().WithName(p.Secret.Name).WithOptional(false)
		for _, item := range p.Secret.Items {
			secret.WithItems(core.KeyToPath().WithKey(item.Key).WithPath(item.Path).WithMode(*item.Mode))
		}
		projected.WithSources(core.VolumeProjection().WithSecret(secret))
	}
	pod := core.PodSpec().WithAutomountServiceAccountToken(false).WithTerminationGracePeriodSeconds(30).
		WithSecurityContext(core.PodSecurityContext().WithRunAsNonRoot(true).WithRunAsUser(979).WithRunAsGroup(979).
			WithFSGroup(979).WithFSGroupChangePolicy(corev1.FSGroupChangeOnRootMismatch).
			WithSeccompProfile(core.SeccompProfile().WithType(corev1.SeccompProfileTypeRuntimeDefault))).
		WithVolumes(
			core.Volume().WithName("code").WithEmptyDir(core.EmptyDirVolumeSource()),
			core.Volume().WithName("uploads").WithPersistentVolumeClaim(core.PersistentVolumeClaimVolumeSource().WithClaimName(Name(c, "uploads"))),
			core.Volume().WithName("temporary").WithEmptyDir(core.EmptyDirVolumeSource().WithSizeLimit(c.Spec.Storage.Temporary.SizeLimit)),
			core.Volume().WithName("php-tmp").WithEmptyDir(core.EmptyDirVolumeSource().WithSizeLimit(resource.MustParse("64Mi"))),
			core.Volume().WithName("php-config").WithConfigMap(core.ConfigMapVolumeSource().WithName(phpConfig).WithDefaultMode(mode)),
			core.Volume().WithName("credentials").WithProjected(projected))
	for _, ref := range c.Spec.Release.ImagePullSecrets {
		pod.WithImagePullSecrets(core.LocalObjectReference().WithName(ref.Name))
	}
	policy := c.Spec.Release.Image.PullPolicy
	if policy == "" {
		policy = corev1.PullIfNotPresent
	}
	init := core.Container().WithName("release").WithImage(image).WithImagePullPolicy(policy).
		WithCommand("/bin/sh", render.ConfigRoot+"/init-release.sh", c.Spec.Storage.Uploads.MountPath).WithSecurityContext(security()).
		WithVolumeMounts(mount("code", "/work/code", false), mount("temporary", "/work/temporary", false), mount("php-config", render.ConfigRoot, true))
	php := core.Container().WithName("php").WithImage(c.Spec.Runtime.Web.PHP.Image).WithImagePullPolicy(corev1.PullIfNotPresent).
		WithCommand("php-fpm", "-y", render.ConfigRoot+"/php-fpm.conf").WithSecurityContext(security()).
		WithResources(resources(c.Spec.Runtime.Web.PHP.Resources)).
		WithVolumeMounts(mount("code", render.Root, true), mount("uploads", c.Spec.Storage.Uploads.MountPath, false),
			mount("temporary", c.Spec.Storage.Temporary.MountPath, false), mount("php-tmp", "/tmp", false),
			mount("php-config", render.ConfigRoot, true), mount("credentials", render.SecretRoot, true),
			mount("php-config", "/usr/local/etc/php/conf.d/zz-operator.ini", true).WithSubPath("php.ini"),
			mount("php-config", render.Root+"/bitrix/license_key.php", true).WithSubPath("license_key.php")).
		WithStartupProbe(phpProbe().WithFailureThreshold(30)).WithLivenessProbe(phpProbe())
	for _, dir := range []string{"cache", "managed_cache", "stack_cache", "html_pages"} {
		php.WithVolumeMounts(mount("temporary", render.Root+"/bitrix/"+dir, false).WithSubPath(dir))
	}
	pod.WithInitContainers(init).WithContainers(php)
	return pod
}

// Deployment mounts code read-only in both web containers and credentials only in PHP.
func Deployment(c *bx.BitrixCluster, b *render.Bundle, revision, image string) *apps.DeploymentApplyConfiguration {
	pod := phpPod(c, b, image)
	php := pod.Containers[0]
	pod.Containers = nil
	nginx := core.Container().WithName("nginx").WithImage(c.Spec.Runtime.Web.Nginx.Image).WithImagePullPolicy(corev1.PullIfNotPresent).
		WithCommand("/bin/sh", "/run/nginx/start.sh").WithSecurityContext(security()).
		WithResources(resources(c.Spec.Runtime.Web.Nginx.Resources)).WithPorts(core.ContainerPort().WithName("http").WithContainerPort(8080)).
		WithVolumeMounts(mount("code", render.Root, true), mount("uploads", c.Spec.Storage.Uploads.MountPath, true),
			mount("nginx-config", "/run/nginx", true), mount("nginx-tmp", "/tmp", false)).
		WithStartupProbe(httpProbe("/live").WithFailureThreshold(30)).WithLivenessProbe(httpProbe("/live")).WithReadinessProbe(httpProbe("/ready"))
	nginx.WithVolumeMounts(mount("temporary", render.Root+"/bitrix/html_pages", true).WithSubPath("html_pages"))
	pod.WithContainers(nginx, &php)
	pod.WithVolumes(
		core.Volume().WithName("nginx-tmp").WithEmptyDir(core.EmptyDirVolumeSource().WithSizeLimit(resource.MustParse("64Mi"))),
		core.Volume().WithName("nginx-config").WithConfigMap(core.ConfigMapVolumeSource().WithName(Name(c, "nginx-"+b.Hash[:12])).WithDefaultMode(0444)))
	replicas := max(c.Spec.Runtime.Web.Replicas, 1)
	if AutoscalingEnabled(c) {
		a := c.Spec.Runtime.Web.Autoscaling
		replicas = min(max(replicas, a.MinReplicas), a.MaxReplicas)
	}
	strategy := apps.DeploymentStrategy().WithType(appsv1.RecreateDeploymentStrategyType)
	for _, mode := range c.Spec.Storage.Uploads.PersistentVolumeClaim.AccessModes {
		if mode == corev1.ReadWriteMany {
			strategy = apps.DeploymentStrategy().WithType(appsv1.RollingUpdateDeploymentStrategyType).
				WithRollingUpdate(apps.RollingUpdateDeployment().WithMaxSurge(intstr.FromInt32(1)).WithMaxUnavailable(intstr.FromInt32(0)))
		}
	}
	return apps.Deployment(Name(c, "web"), c.Namespace).WithLabels(labels(c)).WithOwnerReferences(owner(c)).
		WithSpec(apps.DeploymentSpec().WithReplicas(replicas).WithRevisionHistoryLimit(2).WithProgressDeadlineSeconds(600).
			WithStrategy(strategy).WithSelector(meta.LabelSelector().WithMatchLabels(labels(c))).
			WithTemplate(core.PodTemplateSpec().WithLabels(labels(c)).WithAnnotations(map[string]string{ClusterName: c.Name, ConfigHash: b.Hash, SecretRevision: revision}).WithSpec(pod)))
}

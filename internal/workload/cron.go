package workload

import (
	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	appsv1 "k8s.io/api/apps/v1"
	apps "k8s.io/client-go/applyconfigurations/apps/v1"
	core "k8s.io/client-go/applyconfigurations/core/v1"
	meta "k8s.io/client-go/applyconfigurations/meta/v1"
)

// Cron has a distinct selector: web's existing immutable selector must never match cron Pods.
func Cron(c *bx.BitrixCluster, b *render.Bundle, revision, image string) *apps.DeploymentApplyConfiguration {
	labels := map[string]string{"app.kubernetes.io/name": "bitrix-cron", OwnerUID: string(c.UID)}
	pod := phpPod(c, b, image)
	php := &pod.Containers[0]
	php.WithName("cron")
	php.Command = []string{"php", render.ConfigRoot + "/scheduler.php"}
	probe := func(mode string) *core.ProbeApplyConfiguration {
		return core.Probe().WithExec(core.ExecAction().WithCommand("php", render.ConfigRoot+"/cron-health.php", mode)).WithPeriodSeconds(5).WithTimeoutSeconds(3).WithFailureThreshold(3)
	}
	php.StartupProbe = probe("live").WithFailureThreshold(30)
	php.LivenessProbe = probe("live")
	php.ReadinessProbe = probe("ready")
	return apps.Deployment(Name(c, "cron"), c.Namespace).WithLabels(labels).WithOwnerReferences(owner(c)).WithSpec(
		apps.DeploymentSpec().WithReplicas(1).WithRevisionHistoryLimit(2).WithProgressDeadlineSeconds(600).
			WithStrategy(apps.DeploymentStrategy().WithType(appsv1.RecreateDeploymentStrategyType)).
			WithSelector(meta.LabelSelector().WithMatchLabels(labels)).
			WithTemplate(core.PodTemplateSpec().WithLabels(labels).WithAnnotations(map[string]string{ConfigHash: b.Hash, SecretRevision: revision}).WithSpec(pod)))
}

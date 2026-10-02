package workload

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	corev1 "k8s.io/api/core/v1"
	batch "k8s.io/client-go/applyconfigurations/batch/v1"
	core "k8s.io/client-go/applyconfigurations/core/v1"
)

// SiteValidationName also changes after Secret rotation, even if the CR generation does not.
func SiteValidationName(c *bx.BitrixCluster, b *render.Bundle, revision, release string) string {
	hash := sha256.Sum256(fmt.Appendf(nil, "%s/%d/%s/%s/%s", c.UID, c.Generation, b.Hash, revision, release))
	return Name(c, fmt.Sprintf("sites-%x", hash[:8]))
}

func SiteValidation(c *bx.BitrixCluster, b *render.Bundle, revision, image, release string) *batch.JobApplyConfiguration {
	pod := phpPod(c, b, image)
	// Validation has no need for user uploads and must not contend for an RWO attachment.
	for i := range pod.Volumes {
		if *pod.Volumes[i].Name == "uploads" {
			pod.Volumes[i].PersistentVolumeClaim = nil
			pod.Volumes[i].WithEmptyDir(core.EmptyDirVolumeSource())
		}
	}
	pod.WithRestartPolicy(corev1.RestartPolicyNever)
	php := &pod.Containers[0]
	php.Command = []string{"php", "-d", "display_errors=0", "-d", "log_errors=0", render.ConfigRoot + "/validate-sites.php"}
	php.StartupProbe = nil
	php.LivenessProbe = nil
	identity, _ := json.Marshal(render.ValidationIdentity{ClusterUID: string(c.UID), Generation: c.Generation, Release: release})
	php.WithEnv(core.EnvVar().WithName("BITRIX_VALIDATION_IDENTITY").WithValue(string(identity)),
		core.EnvVar().WithName("BITRIX_VALIDATION_ATTEMPT").WithValueFrom(core.EnvVarSource().WithFieldRef(core.ObjectFieldSelector().WithFieldPath("metadata.labels['batch.kubernetes.io/controller-uid']"))))
	php.WithTerminationMessagePath("/tmp/site-result.json").WithTerminationMessagePolicy(corev1.TerminationMessageReadFile)
	metadata := labels(c)
	metadata[appNameLabel] = "bitrix-sites"
	annotations := map[string]string{ClusterName: c.Name, ConfigHash: b.Hash, SecretRevision: revision}
	return batch.Job(SiteValidationName(c, b, revision, release), c.Namespace).WithLabels(metadata).WithOwnerReferences(owner(c)).
		WithSpec(batch.JobSpec().WithBackoffLimit(0).WithActiveDeadlineSeconds(120).
			WithTemplate(core.PodTemplateSpec().WithLabels(metadata).WithAnnotations(annotations).WithSpec(pod)))
}

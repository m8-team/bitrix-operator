package workload

import (
	corev1 "k8s.io/api/core/v1"
	core "k8s.io/client-go/applyconfigurations/core/v1"
)

// projectedSecrets converts renderer-owned Secret projections to apply configurations.
// Keep the PHP, cron and Push Pod security contracts aligned without copying values.
func projectedSecrets(projections []corev1.VolumeProjection) *core.ProjectedVolumeSourceApplyConfiguration {
	projected := core.ProjectedVolumeSource().WithDefaultMode(0440)
	for _, projection := range projections {
		if projection.Secret == nil {
			continue
		}
		secret := core.SecretProjection().WithName(projection.Secret.Name).WithOptional(false)
		for _, item := range projection.Secret.Items {
			path := core.KeyToPath().WithKey(item.Key).WithPath(item.Path)
			if item.Mode != nil {
				path.WithMode(*item.Mode)
			}
			secret.WithItems(path)
		}
		projected.WithSources(core.VolumeProjection().WithSecret(secret))
	}
	return projected
}

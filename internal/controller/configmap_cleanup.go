package controller

import (
	"context"
	"encoding/hex"
	"slices"
	"strings"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	"github.com/m8-team/bitrix-operator/internal/workload"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// managedConfigMapName restricts collection to versioned runtime configuration.
// Other ConfigMaps are never eligible, even when they carry the cluster labels.
func managedConfigMapName(c *bx.BitrixCluster, name string) bool {
	for _, kind := range []string{"nginx", "php", stagePush} {
		prefix := workload.Name(c, kind+"-")
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		digest := strings.TrimPrefix(name, prefix)
		if len(digest) != 12 {
			return false
		}
		_, err := hex.DecodeString(digest)
		return err == nil
	}
	return false
}

func recordConfigMapReferences(pod corev1.PodSpec, referenced map[string]bool) {
	for _, volume := range pod.Volumes {
		if volume.ConfigMap != nil {
			referenced[volume.ConfigMap.Name] = true
		}
		if volume.Projected != nil {
			for _, source := range volume.Projected.Sources {
				if source.ConfigMap != nil {
					referenced[source.ConfigMap.Name] = true
				}
			}
		}
	}
	for _, container := range append(slices.Clone(pod.InitContainers), pod.Containers...) {
		for _, from := range container.EnvFrom {
			if from.ConfigMapRef != nil {
				referenced[from.ConfigMapRef.Name] = true
			}
		}
		for _, env := range container.Env {
			if env.ValueFrom != nil && env.ValueFrom.ConfigMapKeyRef != nil {
				referenced[env.ValueFrom.ConfigMapKeyRef.Name] = true
			}
		}
	}
}

// pruneConfigMaps never removes a revision referenced by a live workload.
// It runs only after the new web rollout has completed. Every candidate is
// checked against the uncached API before deletion, with UID/RV preconditions.
func (r *BitrixClusterReconciler) pruneConfigMaps(ctx context.Context, c *bx.BitrixCluster, b *render.Bundle) error {
	configs := &corev1.ConfigMapList{}
	label := client.MatchingLabels{workload.OwnerUID: string(c.UID)}
	if err := r.reader().List(ctx, configs, client.InNamespace(c.Namespace), label); err != nil {
		return err
	}
	keep := map[string]bool{
		workload.Name(c, "nginx-"+b.Hash[:12]): true,
		workload.Name(c, "php-"+b.Hash[:12]):   true,
	}
	if pushEnabled(c) {
		keep[workload.Name(c, "push-"+b.Hash[:12])] = true
	}
	candidates := make([]*corev1.ConfigMap, 0)
	for i := range configs.Items {
		cm := &configs.Items[i]
		if !managedConfigMapName(c, cm.Name) || keep[cm.Name] || !controlledByCluster(c, cm) || !cm.DeletionTimestamp.IsZero() {
			continue
		}
		candidates = append(candidates, cm)
	}
	if len(candidates) == 0 {
		return nil
	}
	referenced := map[string]bool{}
	deployments := &appsv1.DeploymentList{}
	if err := r.reader().List(ctx, deployments, client.InNamespace(c.Namespace), label); err != nil {
		return err
	}
	for i := range deployments.Items {
		recordConfigMapReferences(deployments.Items[i].Spec.Template.Spec, referenced)
	}
	replicasets := &appsv1.ReplicaSetList{}
	if err := r.reader().List(ctx, replicasets, client.InNamespace(c.Namespace), label); err != nil {
		return err
	}
	for i := range replicasets.Items {
		recordConfigMapReferences(replicasets.Items[i].Spec.Template.Spec, referenced)
	}
	jobs := &batchv1.JobList{}
	if err := r.reader().List(ctx, jobs, client.InNamespace(c.Namespace), label); err != nil {
		return err
	}
	for i := range jobs.Items {
		recordConfigMapReferences(jobs.Items[i].Spec.Template.Spec, referenced)
	}
	pods := &corev1.PodList{}
	if err := r.reader().List(ctx, pods, client.InNamespace(c.Namespace), label); err != nil {
		return err
	}
	for i := range pods.Items {
		recordConfigMapReferences(pods.Items[i].Spec, referenced)
	}
	for _, cm := range candidates {
		if referenced[cm.Name] {
			continue
		}
		uid, rv := cm.UID, cm.ResourceVersion
		if err := client.IgnoreNotFound(r.Delete(ctx, cm, client.Preconditions{UID: &uid, ResourceVersion: &rv})); err != nil {
			return err
		}
	}
	return nil
}

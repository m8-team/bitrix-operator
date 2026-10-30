package controller

import (
	"context"
	"errors"
	"encoding/json"
	"fmt"
	"slices"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"github.com/m8-team/bitrix-operator/internal/kubeops"
	"github.com/m8-team/bitrix-operator/internal/workload"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/csaupgrade"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ResourceManager handles declarative Kubernetes writes and persistent storage.
// Unlike the hash-based ClickHouse manager, it retains Bitrix SSA/UID/managedFields
// safety guarantees and never adopts foreign resources.
type ResourceManager struct {
	client client.Client
	reader client.Reader
}

func (r *BitrixClusterReconciler) resourceManager() ResourceManager {
	return ResourceManager{
		client: r.Client,
		reader: r.reader(),
	}
}

const replicaHandoffManager = "bitrix-operator-replica-handoff"

// Apply accepts only declaratively built configurations, never a serialized live object.
// The create path and resourceVersion precondition prevent accidental adoption on races.
func (rm ResourceManager) Apply(ctx context.Context, c *bx.BitrixCluster, configuration any) error {
	data, err := json.Marshal(configuration)
	if err != nil {
		return err
	}
	desired := &unstructured.Unstructured{}
	if err = json.Unmarshal(data, &desired.Object); err != nil {
		return err
	}
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(desired.GroupVersionKind())
	err = rm.reader.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		// Create first to reject a concurrent foreign object instead of adopting it via apply.
		created := desired.DeepCopy()
		if err = rm.client.Create(ctx, created, client.FieldOwner("bitrix-operator")); err != nil {
			return err
		}
		existing = created
		err = nil
	}
	if err != nil {
		return err
	}
	if !controlledByCluster(c, existing) {
		return block("ResourceCollision", fmt.Sprintf("%s %s is not controlled by this BitrixCluster", desired.GetKind(), desired.GetName()))
	}
	if !existing.GetDeletionTimestamp().IsZero() {
		return waitFor("ResourceDeleting", "An application resource is being deleted", pendingRequeueAfter)
	}
	if workload.AutoscalingEnabled(c) && desired.GetKind() == deploymentKind && desired.GetName() == workload.Name(c, "web") {
		if err = rm.handoffReplicas(ctx, existing); err != nil {
			return err
		}
		unstructured.RemoveNestedField(desired.Object, "spec", "replicas")
	}
	// Create uses an Update managed-fields entry. ForceOwnership alone does not
	// remove its shared ownership of unchanged fields, so later omissions could
	// leave stale limits, pull secrets or volume settings behind. Migrate only
	// our own entry with the official client-go helper and its RV precondition.
	patch, err := csaupgrade.UpgradeManagedFieldsPatch(existing, sets.New("bitrix-operator"), "bitrix-operator")
	if err != nil {
		return err
	}
	if patch != nil {
		if err = rm.client.Patch(ctx, existing, client.RawPatch(types.JSONPatchType, patch)); err != nil {
			return err
		}
	}
	desired.SetResourceVersion(existing.GetResourceVersion())
	// These fields are controlled by the operator. Force only on an owned object,
	// guarded by resourceVersion; unrelated fields from other managers remain intact.
	return rm.client.Apply(ctx, client.ApplyConfigurationFromUnstructured(desired), client.FieldOwner("bitrix-operator"), client.ForceOwnership)
}

func (rm ResourceManager) Storage(ctx context.Context, c *bx.BitrixCluster) (*corev1.PersistentVolumeClaim, error) {
	desired := workload.PVC(c)
	class := &storagev1.StorageClass{}
	if err := rm.reader.Get(ctx, client.ObjectKey{Name: *desired.Spec.StorageClassName}, class); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, block("StorageClassMissing", "The requested StorageClass does not exist")
		}
		return nil, err
	}
	live := &corev1.PersistentVolumeClaim{}
	err := rm.reader.Get(ctx, client.ObjectKeyFromObject(desired), live)
	if apierrors.IsNotFound(err) {
		err = rm.client.Create(ctx, desired, client.FieldOwner("bitrix-operator"))
		return desired, err
	}
	if err != nil {
		return nil, err
	}
	if live.Labels[workload.OwnerUID] != string(c.UID) || live.Annotations[workload.ClusterName] != c.Name || len(live.OwnerReferences) != 0 {
		return nil, block("StorageCollision", "The uploads PVC is not reserved for this cluster UID; explicit data recovery is required")
	}
	if !live.DeletionTimestamp.IsZero() {
		return nil, block("StorageDeleting", "The uploads PVC is being deleted")
	}
	actualModes := slices.Clone(live.Spec.AccessModes)
	requestedModes := slices.Clone(desired.Spec.AccessModes)
	slices.Sort(actualModes)
	slices.Sort(requestedModes)
	if live.Spec.StorageClassName == nil || *live.Spec.StorageClassName != *desired.Spec.StorageClassName || !slices.Equal(actualModes, requestedModes) ||
		(live.Spec.VolumeMode != nil && *live.Spec.VolumeMode != corev1.PersistentVolumeFilesystem) {
		return nil, block("ImmutableStorage", "Changing the uploads storage class, access modes or volume mode requires data migration")
	}
	size := live.Spec.Resources.Requests[corev1.ResourceStorage]
	want := desired.Spec.Resources.Requests[corev1.ResourceStorage]
	if want.Cmp(size) < 0 {
		return nil, block("StorageShrinkForbidden", "Shrinking the uploads PVC is not supported")
	}
	if want.Cmp(size) > 0 {
		if class.AllowVolumeExpansion == nil || !*class.AllowVolumeExpansion {
			return nil, block("StorageExpansionForbidden", "The StorageClass does not allow expansion")
		}
		before := live.DeepCopy()
		live.Spec.Resources.Requests[corev1.ResourceStorage] = want
		if err = rm.client.Patch(ctx, live, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return nil, err
		}
	}
	return live, nil
}

 // DeleteOwned guards deletion by the exact BitrixCluster controller UID.
func (rm ResourceManager) DeleteOwned(ctx context.Context, c *bx.BitrixCluster, obj client.Object, foreground bool) (bool, error) {
	found, err := kubeops.DeleteOwned(ctx, rm.reader, rm.client, obj, func(live client.Object) bool {
		return controlledByCluster(c, live)
	}, foreground)
	if errors.Is(err, kubeops.ErrForeignResource) {
		return found, block("ResourceCollision", "An application resource is not controlled by this BitrixCluster")
	}
	return found, err
}

// Transfer a shared replica field to a temporary SSA manager before omission by
// the regular operator manager. This avoids defaulting to 1. The HPA's first scale
// update takes ownership away from the temporary manager. Never repeat after the
// regular manager has relinquished the field, including after operator restarts.
func (rm ResourceManager) handoffReplicas(ctx context.Context, live *unstructured.Unstructured) error {
	owns := false
	for _, field := range live.GetManagedFields() {
		if field.Manager != "bitrix-operator" || field.Subresource != "" || field.FieldsV1 == nil {
			continue
		}
		var fields map[string]any
		if err := json.Unmarshal(field.FieldsV1.GetRawBytes(), &fields); err != nil {
			return err
		}
		if _, found, err := unstructured.NestedFieldNoCopy(fields, "f:spec", "f:replicas"); err != nil {
			return err
		} else if found {
			owns = true
		}
	}
	if !owns {
		return nil
	}
	replicas, found, err := unstructured.NestedInt64(live.Object, "spec", "replicas")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("deployment replicas missing during handoff")
	}
	handoff := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": deploymentKind,
		"metadata": map[string]any{"name": live.GetName(), "namespace": live.GetNamespace(), "resourceVersion": live.GetResourceVersion()},
		"spec":     map[string]any{"replicas": replicas},
	}}
	if err = rm.client.Apply(ctx, client.ApplyConfigurationFromUnstructured(handoff), client.FieldOwner(replicaHandoffManager)); err != nil {
		return err
	}
	uid := live.GetUID()
	if err = rm.reader.Get(ctx, client.ObjectKeyFromObject(live), live); err != nil {
		return err
	}
	if live.GetUID() != uid {
		return block("ResourceCollision", "The Deployment was replaced during replica ownership handoff")
	}
	return nil
}

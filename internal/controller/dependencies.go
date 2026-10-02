package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"

	"unicode/utf8"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	"github.com/m8-team/bitrix-operator/internal/workload"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
)

const secretIndex = "bitrix.dependencies.secrets"
const storageClassIndex = "bitrix.dependencies.storageClass"

func secretNames(obj client.Object) []string {
	c := obj.(*bx.BitrixCluster)
	names := make([]string, 0, 3+len(c.Spec.Database.Connections)+len(c.Spec.Release.ImagePullSecrets))
	names = append(names, c.Spec.License.SecretRef.Name, c.Spec.Crypto.SecretRef.Name, c.Spec.Redis.PasswordSecretRef.Name)
	for _, connection := range c.Spec.Database.Connections {
		names = append(names, connection.PasswordSecretRef.Name)
	}
	if p := c.Spec.Runtime.Push; p != nil && p.Enabled {
		names = append(names, p.SecretRef.Name)
	}
	for _, ref := range c.Spec.Release.ImagePullSecrets {
		names = append(names, ref.Name)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// Only metadata enters the rollout revision; credentials never enter logs, status or hashes.
func (r *BitrixClusterReconciler) secrets(ctx context.Context, c *bx.BitrixCluster, b *render.Bundle) (string, error) {
	hash := sha256.New()
	for _, name := range secretNames(c) {
		secret := &corev1.Secret{}
		if err := r.reader().Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: name}, secret); err != nil {
			if apierrors.IsNotFound(err) {
				return "", block("SecretMissing", "A referenced Secret does not exist")
			}
			return "", err
		}
		for _, projection := range b.Secrets {
			if projection.Secret.Name == name {
				for _, item := range projection.Secret.Items {
					if len(secret.Data[item.Key]) == 0 || len(secret.Data[item.Key]) > 1<<20 {
						return "", block("SecretKeyInvalid", "A referenced Secret key is absent, empty or exceeds the size limit")
					}
				}
			}
		}
		if p := c.Spec.Runtime.Push; p != nil && p.Enabled && p.SecretRef.Name == name {
			value := secret.Data[p.SecretRef.Key]
			if len(value) < 32 || len(value) > 512 || !utf8.Valid(value) {
				return "", block("SecretKeyInvalid", "Push signing key must contain 32–512 bytes of valid UTF-8")
			}
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%s\x00", name, secret.UID, secret.ResourceVersion)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func (r *BitrixClusterReconciler) dependencyRequests(index string, namespaced bool) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []ctrl.Request {
		list := &bx.BitrixClusterList{}
		options := []client.ListOption{client.MatchingFields{index: obj.GetName()}}
		if namespaced {
			options = append(options, client.InNamespace(obj.GetNamespace()))
		}
		if err := r.List(ctx, list, options...); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "Could not list dependent clusters")
			return nil
		}
		requests := make([]ctrl.Request, 0, len(list.Items))
		for _, c := range list.Items {
			requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&c)})
		}
		return requests
	}
}

func pvcRequest(_ context.Context, obj client.Object) []ctrl.Request {
	name := obj.GetAnnotations()[workload.ClusterName]
	if name == "" {
		return nil
	}
	return []ctrl.Request{{NamespacedName: client.ObjectKey{Namespace: obj.GetNamespace(), Name: name}}}
}

// SetupWithManager uses metadata-only Secret watches, keeping credential data out of the shared cache.
func (r *BitrixClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.APIReader = mgr.GetAPIReader()
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &bx.BitrixCluster{}, secretIndex, secretNames); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &bx.BitrixCluster{}, storageClassIndex, func(obj client.Object) []string {
		return []string{obj.(*bx.BitrixCluster).Spec.Storage.Uploads.PersistentVolumeClaim.StorageClassName}
	}); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).For(&bx.BitrixCluster{}).
		Owns(deploymentObject()).Owns(&corev1.Service{}).Owns(&corev1.ConfigMap{}).Owns(&networkingv1.NetworkPolicy{}).
		WatchesMetadata(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.dependencyRequests(secretIndex, true))).
		Watches(&storagev1.StorageClass{}, handler.EnqueueRequestsFromMapFunc(r.dependencyRequests(storageClassIndex, false))).
		Watches(&corev1.PersistentVolumeClaim{}, handler.EnqueueRequestsFromMapFunc(pvcRequest)).
		Named("bitrixcluster").Complete(r)
}

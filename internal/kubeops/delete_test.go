package kubeops

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type trackingClient struct {
	client.Client
	deletes int
	options client.DeleteOptions
}

func (c *trackingClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	c.deletes++
	c.options = client.DeleteOptions{}
	c.options.ApplyOptions(opts)
	return c.Client.Delete(ctx, obj, opts...)
}

func TestGuardedDeleteWithoutAPIServer(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	owner := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "parent", Namespace: "default", UID: types.UID("uid-parent")}}
	ours := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "ours", Namespace: "default", UID: types.UID("uid-ours"),
		OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: owner.Name, UID: owner.UID, Controller: ptr.To(true)}}}}
	other := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "default"}}
	writer := &trackingClient{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(ours, other).Build()}
	owns := func(o client.Object) bool { return ControlledBy(o, owner, "v1", "ConfigMap") }
	missing := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "absent", Namespace: "default"}}
	found, err := DeleteOwned(ctx, writer, writer, missing, owns, true)
	if err != nil || found {
		t.Fatalf("missing object: found=%v err=%v", found, err)
	}
	foreign := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "default"}}
	found, err = DeleteOwned(ctx, writer, writer, foreign, owns, false)
	if !found || !errors.Is(err, ErrForeignResource) || writer.deletes != 0 {
		t.Fatalf("foreign object: found=%v err=%v deletes=%d", found, err, writer.deletes)
	}
	live := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "ours", Namespace: "default"}}
	found, err = DeleteOwned(ctx, writer, writer, live, owns, true)
	if err != nil || !found || writer.deletes != 1 {
		t.Fatalf("owned object: found=%v err=%v deletes=%d", found, err, writer.deletes)
	}
	if writer.options.Preconditions == nil || writer.options.Preconditions.UID == nil || *writer.options.Preconditions.UID != ours.UID || writer.options.Preconditions.ResourceVersion == nil || writer.options.PropagationPolicy == nil || *writer.options.PropagationPolicy != metav1.DeletePropagationForeground {
		t.Fatalf("unguarded deletion: %#v", writer.options)
	}
}


func TestControlledByVerifiesIdentity(t *testing.T) {
	owner := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{UID: types.UID("owner")}}
	obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{UID: owner.UID, Kind: "ConfigMap", APIVersion: "v1", Controller: ptr.To(true)}}}}
	if !ControlledBy(obj, owner, "v1", "ConfigMap") || ControlledBy(obj, owner, "apps/v1", "ConfigMap") || ControlledBy(obj, owner, "v1", "Secret") {
		t.Fatal("unexpected owner validation result")
	}
}

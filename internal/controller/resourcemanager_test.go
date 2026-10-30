package controller

import (
	"context"
	"errors"
	"testing"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestResourceManagerRefusesForeignResource(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	foreign := &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "tenant"},
		Data:       map[string]string{"setting": "foreign"},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(foreign).Build()
	cluster := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Name: "portal", Namespace: "tenant"}}
	manager := ResourceManager{client: kube, reader: kube}
	desired := foreign.DeepCopy()
	desired.Data["setting"] = "operator"
	err := manager.Apply(ctx, cluster, desired)
	var collision *blocked
	if !errors.As(err, &collision) || collision.reason != "ResourceCollision" {
		t.Fatalf("expected ResourceCollision, got %v", err)
	}
	live := &corev1.ConfigMap{}
	if err := kube.Get(ctx, client.ObjectKeyFromObject(foreign), live); err != nil {
		t.Fatal(err)
	}
	if live.Data["setting"] != "foreign" {
		t.Fatalf("foreign ConfigMap modified: %+v", live.Data)
	}
}

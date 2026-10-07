package controller

import (
	"context"
	"errors"
	"testing"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestTransitionEvents(t *testing.T) {
	fake := events.NewFakeRecorder(10)
	r := &BitrixClusterReconciler{Recorder: fake}
	before := &bx.BitrixCluster{}
	current := before.DeepCopy()
	current.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionFalse, Reason: "credential-do-not-export", Message: "credential-do-not-export"}}
	for _, tc := range []struct {
		phase  bx.BitrixClusterPhase
		status metav1.ConditionStatus
		want   string
	}{
		{bx.BitrixClusterPhaseProgressing, metav1.ConditionFalse, "Normal ClusterProgressing BitrixCluster is progressing; inspect status.conditions for component details"},
		{bx.BitrixClusterPhaseDegraded, metav1.ConditionFalse, "Warning ClusterDegraded BitrixCluster is degraded; inspect status.conditions for component details"},
		{bx.BitrixClusterPhaseReady, metav1.ConditionTrue, "Normal ClusterReady The current runtime and requested Bitrix site IDs are ready"},
	} {
		current.Status.Phase, current.Status.Conditions[0].Status = tc.phase, tc.status
		r.recordTransition(before, current)
		select {
		case got := <-fake.Events:
			if got != tc.want {
				t.Fatalf("event=%q; want %q", got, tc.want)
			}
		default:
			t.Fatal("missing transition event")
		}
		before = current.DeepCopy()
		(&BitrixClusterReconciler{Recorder: fake}).recordTransition(before, current)
		select {
		case extra := <-fake.Events:
			t.Fatalf("duplicate: %s", extra)
		default:
		}
	}
}

type failedStatusClient struct{ client.Client }

func (c failedStatusClient) Status() client.SubResourceWriter {
	return failedStatusWriter{c.Client.Status()}
}

type failedStatusWriter struct{ client.SubResourceWriter }

func (failedStatusWriter) Patch(context.Context, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
	return errors.New("status write failed")
}

var _ = Describe("Persisted transition events", func() {
	It("does not emit when a status write fails and emits once after retry", func() {
		c := webSample()
		fake := events.NewFakeRecorder(10)
		r := &BitrixClusterReconciler{Client: failedStatusClient{k8sClient}, Recorder: fake}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(c)}
		_, err := r.Reconcile(ctx, request)
		Expect(err).To(MatchError("status write failed"))
		Expect(fake.Events).To(BeEmpty())
		r.Client = k8sClient
		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.Events).To(HaveLen(1))
		<-fake.Events
		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.Events).To(BeEmpty())
	})
})

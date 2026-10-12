package observability

import (
	"context"
	"errors"
	"strings"
	"testing"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := bx.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&bx.BitrixCluster{}).WithObjects(objects...).Build()
}

func sampleCluster() *bx.BitrixCluster {
	return &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Name: "portal", Namespace: "tenant", Generation: 2},
		Status: bx.BitrixClusterStatus{ObservedGeneration: 2, Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, ObservedGeneration: 2, Reason: "SecretValueMustNotLeak", Message: "credential-do-not-export"}}}}
}

func TestConditionFreshness(t *testing.T) {
	for _, mode := range []string{"current", "stale-condition", "stale-status", "missing", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			c := sampleCluster()
			switch mode {
			case "stale-condition":
				c.Status.Conditions[0].ObservedGeneration = 1
			case "stale-status":
				c.Status.ObservedGeneration = 1
			case "missing":
				c.Status.Conditions = nil
			case "invalid":
				c.Status.Conditions[0].Status = "not-a-status"
			}
			registry := prometheus.NewPedanticRegistry()
			registry.MustRegister(NewCollector(testClient(t, c)))
			families, err := registry.Gather()
			if err != nil {
				t.Fatal(err)
			}
			count, active := 0, ""
			for _, f := range families {
				if strings.Contains(f.String(), "credential") || strings.Contains(f.String(), "SecretValue") {
					t.Fatal("sensitive status exported")
				}
				if f.GetName() != "bitrix_cluster_condition" {
					continue
				}
				for _, metric := range f.Metric {
					count++
					labels := map[string]string{}
					for _, l := range metric.Label {
						labels[l.GetName()] = l.GetValue()
					}
					if labels["condition"] == "Ready" && metric.Gauge.GetValue() == 1 {
						active = labels["status"]
					}
				}
			}
			want := "Unknown"
			if mode == "current" {
				want = "True"
			}
			if active != want || count != 24 {
				t.Fatalf("status=%s, samples=%d; want %s/24", active, count, want)
			}
		})
	}
}

func TestCollectionLifecycle(t *testing.T) {
	c := sampleCluster()
	c.Status.Runtime = &bx.RuntimeStatus{Web: bx.WorkloadStatus{DesiredReplicas: 4, ReadyReplicas: 3}}
	reader := testClient(t, c)
	collector := NewCollector(reader)
	if count := testutil.CollectAndCount(collector, "bitrix_cluster_replicas"); count != 16 {
		t.Fatalf("replica samples=%d", count)
	}
	if problems, err := testutil.CollectAndLint(collector); err != nil || len(problems) != 0 {
		t.Fatalf("lint: %v, %v", problems, err)
	}
	if err := reader.Delete(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	if count := testutil.CollectAndCount(collector, "bitrix_cluster_condition", "bitrix_cluster_replicas", "bitrix_cluster_generation"); count != 0 {
		t.Fatal("deleted cluster retains series")
	}
	c.DeletionTimestamp = new(metav1.Now())
	c.Finalizers = []string{"test/hold"}
	if count := testutil.CollectAndCount(NewCollector(testClient(t, c)), "bitrix_cluster_condition"); count != 0 {
		t.Fatal("terminating cluster retains series")
	}
	c.DeletionTimestamp, c.Finalizers = nil, nil
	if count := testutil.CollectAndCount(NewCollector(testClient(t, c)), "bitrix_cluster_condition"); count != 24 {
		t.Fatal("restart lost current state")
	}
}

type failingReader struct{ client.Reader }

func (failingReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return errors.New("credential-do-not-export")
}

func TestCollectionFailure(t *testing.T) {
	c := NewCollector(failingReader{})
	if err := testutil.CollectAndCompare(c, strings.NewReader(`# HELP bitrix_operator_collection_success Whether the cluster cache was read successfully (1 or 0).
# TYPE bitrix_operator_collection_success gauge
bitrix_operator_collection_success 0
`), "bitrix_operator_collection_success", "bitrix_cluster_condition"); err != nil {
		t.Fatal(err)
	}
}

package controller

import (
	"context"
	"errors"
	"slices"
	"testing"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)
func TestRuntimeComponentOrder(t *testing.T) {
	// The order is an operational contract: policy/ConfigMaps precede Pods,
	// status observes the newly applied workload, and GC runs last.
	r := &BitrixClusterReconciler{}
	components := r.runtimeComponents()
	got := make([]string, 0, len(components))
	for _, c := range components {
		got = append(got, c.Name())
	}
	want := []string{"config", "push", "web", "cron", "web-status", "sites", "config-gc"}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected runtime plan: %v; want %v", got, want)
	}
}

func TestRuntimeComponentsAreIndependentlyInjectable(t *testing.T) {
	stop := errors.New("component unavailable")
	run := []string{}
	makeStep := func(name string, err error) runtimeComponent {
		return componentStep{name: name, run: func(_ context.Context, _ *bx.BitrixCluster, _ *runtimeInputs) error {
			run = append(run, name)
			return err
		}}
	}
	components := []runtimeComponent{makeStep("storage", nil), makeStep("web", stop), makeStep("sites", nil)}
	if _, err := runRuntimeComponents(context.Background(), &bx.BitrixCluster{}, &runtimeInputs{}, components); !errors.Is(err, stop) {
		t.Fatalf("expected component failure, got %v", err)
	}
	if want := []string{"storage", "web"}; !slices.Equal(run, want) {
		t.Fatalf("unexpected stages after failure: %v", run)
	}
	run = nil
	if _, err := runRuntimeComponents(context.Background(), &bx.BitrixCluster{}, &runtimeInputs{}, components[:1]); err != nil {
		t.Fatalf("isolated component failed: %v", err)
	}
	if !slices.Equal(run, []string{"storage"}) {
		t.Fatalf("isolated component order: %v", run)
	}
}

func TestRuntimeComponentsHonorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	c := componentStep{name: "unreachable", run: func(context.Context, *bx.BitrixCluster, *runtimeInputs) error {
		called = true
		return nil
	}}
	if _, err := runRuntimeComponents(ctx, &bx.BitrixCluster{}, &runtimeInputs{}, []runtimeComponent{c}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if called {
		t.Fatal("component ran after cancellation")
	}
}

func TestRuntimeComponentsAggregateResultsOnPartialFailure(t *testing.T) {
	failed := errors.New("configmap cleanup unavailable")
	c := &bx.BitrixCluster{}
	initializeReconcileStatus(c)
	components := []runtimeComponent{
		componentStep{name: "web-status", run: func(_ context.Context, observed *bx.BitrixCluster, _ *runtimeInputs) error {
			condition(observed, "WebReady", metav1.ConditionTrue, "RolloutComplete", "web is ready")
			return nil
		}},
		componentStep{name: "config-gc", run: func(_ context.Context, observed *bx.BitrixCluster, _ *runtimeInputs) error {
			// A failing component must not overwrite the preceding observation.
			condition(observed, "StorageReady", metav1.ConditionTrue, "VolumeBound", "storage is ready")
			return failed
		}},
	}
	results, err := runRuntimeComponents(context.Background(), c, &runtimeInputs{}, components)
	if !errors.Is(err, failed) || !failedComponent(err, "config-gc") {
		t.Fatalf("missing attributed GC failure: %v", err)
	}
	if len(results) != 2 || results[0].Name != "web-status" || results[1].Name != "config-gc" {
		t.Fatalf("unexpected component results: %+v", results)
	}
	if !meta.IsStatusConditionTrue(c.Status.Conditions, "WebReady") ||
		!meta.IsStatusConditionTrue(c.Status.Conditions, "StorageReady") {
		t.Fatalf("lost observations after an error: %+v", c.Status.Conditions)
	}
	result, returnedErr := resolveReconcileResult(c, err, nil)
	if !errors.Is(returnedErr, failed) || result.RequeueAfter != 0 {
		t.Fatalf("GC API errors must use the rate-limited queue: result=%+v err=%v", result, returnedErr)
	}
	if !meta.IsStatusConditionTrue(c.Status.Conditions, "WebReady") ||
		meta.IsStatusConditionTrue(c.Status.Conditions, "Ready") ||
		c.Status.Phase != bx.BitrixClusterPhaseDegraded {
		t.Fatalf("GC failure must not change observed web readiness: %+v", c.Status)
	}
}

func TestComponentStepDoesNotMutateSharedStatus(t *testing.T) {
	c := &bx.BitrixCluster{}
	initializeReconcileStatus(c)
	step := componentStep{name: "web-status", run: func(_ context.Context, isolated *bx.BitrixCluster, _ *runtimeInputs) error {
		condition(isolated, "WebReady", metav1.ConditionTrue, "RolloutComplete", "ready")
		return nil
	}}
	observation, err := step.Reconcile(context.Background(), c, &runtimeInputs{})
	if err != nil {
		t.Fatal(err)
	}
	if meta.IsStatusConditionTrue(c.Status.Conditions, "WebReady") {
		t.Fatal("component mutated caller's CR before aggregation")
	}
	applyComponentResult(c, observation)
	if !meta.IsStatusConditionTrue(c.Status.Conditions, "WebReady") {
		t.Fatal("missing web readiness after aggregation")
	}
}

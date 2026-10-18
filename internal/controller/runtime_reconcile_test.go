package controller

import (
	"context"
	"errors"
	"slices"
	"testing"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
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
	if err := runRuntimeComponents(context.Background(), &bx.BitrixCluster{}, &runtimeInputs{}, components); !errors.Is(err, stop) {
		t.Fatalf("expected component failure, got %v", err)
	}
	if want := []string{"storage", "web"}; !slices.Equal(run, want) {
		t.Fatalf("unexpected stages after failure: %v", run)
	}
	run = nil
	if err := runRuntimeComponents(context.Background(), &bx.BitrixCluster{}, &runtimeInputs{}, components[:1]); err != nil {
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
	if err := runRuntimeComponents(ctx, &bx.BitrixCluster{}, &runtimeInputs{}, []runtimeComponent{c}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if called {
		t.Fatal("component ran after cancellation")
	}
}

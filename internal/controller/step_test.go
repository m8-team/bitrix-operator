package controller

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileStepsBlockedStillRunsIndependentObservations(t *testing.T) {
	c := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Generation: 8}}
	calls := []string{}
	makeStep := func(name string, always bool, outcome StepResult) ReconcileStep {
		return ReconcileStep{
			Name: name, Always: always,
			Run: func(context.Context) (ComponentResult, StepResult, error) {
				calls = append(calls, name)
				observation := ComponentResult{Name: name}
				if name == "diagnostics" {
					observation.Conditions = []metav1.Condition{{Type: "StorageReady", Status: metav1.ConditionTrue, Reason: "Observed", ObservedGeneration: c.Generation}}
				}
				return observation, outcome, nil
			},
		}
	}
	steps := []ReconcileStep{
		makeStep("dependency", false, StepBlocked(45*time.Second)),
		makeStep("workload", false, StepContinue()),
		makeStep("diagnostics", true, StepRequeue(20*time.Second)),
	}
	out, err := RunReconcileSteps(context.Background(), &StatusManager{cluster: c}, steps)
	if err != nil || !slices.Equal(calls, []string{"dependency", "diagnostics"}) {
		t.Fatalf("blocked stage order: calls=%v err=%v", calls, err)
	}
	if out.RequeueAfter != 20*time.Second || len(out.Results) != 2 ||
		!meta.IsStatusConditionTrue(c.Status.Conditions, "StorageReady") {
		t.Fatalf("observations/requeue lost: %+v %+v", out, c.Status)
	}
}

func TestReconcileStepsErrorPreservesPartialStatus(t *testing.T) {
	stop := errors.New("upstream API unavailable")
	c := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Generation: 4}}
	called := false
	steps := []ReconcileStep{
		{Name: "observe", Run: func(context.Context) (ComponentResult, StepResult, error) {
			return ComponentResult{Conditions: []metav1.Condition{{Type: "WebReady", Status: metav1.ConditionTrue, Reason: "Observed", ObservedGeneration: 4}}}, StepContinue(), stop
		}},
		{Name: "skip", Always: true, Run: func(context.Context) (ComponentResult, StepResult, error) {
			called = true
			return ComponentResult{}, StepContinue(), nil
		}},
	}
	out, err := RunReconcileSteps(context.Background(), &StatusManager{cluster: c}, steps)
	if !errors.Is(err, stop) || !failedComponent(err, "observe") || called || len(out.Results) != 1 {
		t.Fatalf("step error semantics: %+v %v called=%v", out, err, called)
	}
	if !meta.IsStatusConditionTrue(c.Status.Conditions, "WebReady") {
		t.Fatal("lost observation before error")
	}
}

func TestReconcileStepsStopOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	steps := []ReconcileStep{{Name: "skip", Run: func(context.Context) (ComponentResult, StepResult, error) {
		called = true
		return ComponentResult{}, StepContinue(), nil
	}}}
	_, err := RunReconcileSteps(ctx, &StatusManager{cluster: &bx.BitrixCluster{}}, steps)
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("cancelled stage ran: %v called=%v", err, called)
	}
}

func TestReconcileStepsBlockedWithoutDelayUsesSafePoll(t *testing.T) {
	steps := []ReconcileStep{{Name: "waiting", Run: func(context.Context) (ComponentResult, StepResult, error) {
		return ComponentResult{}, StepBlocked(0), nil
	}}}
	out, err := RunReconcileSteps(context.Background(), &StatusManager{cluster: &bx.BitrixCluster{}}, steps)
	if err != nil || out.ControllerResult().RequeueAfter != pendingRequeueAfter {
		t.Fatalf("blocked retry: %+v %v", out, err)
	}
}

package controller

import (
	"context"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
)

// StepResult distinguishes a successful wait from an unexpected API error.
// Blocked skips dependent steps; independent Always steps can still observe.
type StepResult struct {
	RequeueAfter time.Duration
	Blocked      bool
}

func StepContinue() StepResult { return StepResult{} }
func StepRequeue(after time.Duration) StepResult { return StepResult{RequeueAfter: after} }
func StepBlocked(after time.Duration) StepResult { return StepResult{RequeueAfter: after, Blocked: true} }

// ReconcileStep is a named stage returning isolated observations.
// The runner, not the stage, applies these observations to status.
type ReconcileStep struct {
	Name   string
	Always bool
	Run    func(context.Context) (ComponentResult, StepResult, error)
}

type StepRunResult struct {
	Results      []ComponentResult
	RequeueAfter time.Duration
}

// RunReconcileSteps aggregates partial observations before returning errors.
// On error, callers must rely on controller-runtime rate limiting instead
// of RequeueAfter. The first error always short-circuits, even for Always steps.
func RunReconcileSteps(ctx context.Context, status *StatusManager, steps []ReconcileStep) (StepRunResult, error) {
	out := StepRunResult{Results: make([]ComponentResult, 0, len(steps))}
	blocked := false
	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if blocked && !step.Always {
			continue
		}
		observation, result, err := step.Run(ctx)
		if observation.Name == "" {
			observation.Name = step.Name
		}
		status.Apply(observation)
		out.Results = append(out.Results, observation)
		if err != nil {
			return out, &componentError{name: step.Name, cause: err}
		}
		if result.Blocked {
			blocked = true
			if result.RequeueAfter == 0 {
				result.RequeueAfter = pendingRequeueAfter
			}
		}
		out.RequeueAfter = shorterRequeue(out.RequeueAfter, result.RequeueAfter)
	}
	return out, nil
}

// ControllerResult makes the shortest requested retry available to a coordinator.
func (result StepRunResult) ControllerResult() ctrl.Result {
	return ctrl.Result{RequeueAfter: result.RequeueAfter}
}

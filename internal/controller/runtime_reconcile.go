package controller

import (
	"context"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	"github.com/m8-team/bitrix-operator/internal/workload"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	stagePush               = "push"
	stageWeb                = "web"
	stageCron               = "cron"
	stageWebStatus          = "web-status"
	stageSites              = "sites"
	conditionWebReady       = "WebReady"
	conditionSitesValidated = "SitesValidated"
	conditionPushReady      = "PushReady"
	conditionCronReady      = "CronReady"
)

// runtimeDesiredState describes the configuration we intend to apply. It is
// prepared once and never updated by any reconciliation stage.
type runtimeDesiredState struct {
	bundle   *render.Bundle
	revision string
	image    string
}

// runtimeObservedState is a snapshot of dependencies read from the API. Runtime
// rollout observations are reported via ComponentResult, not mutable inputs.
type runtimeObservedState struct {
	storageBound bool
}

type runtimeInputs struct {
	desired  runtimeDesiredState
	observed runtimeObservedState
}

// runtimeComponent is the narrow interface for one independently testable
// reconciliation step. It is intentionally local to the controller: rendering
// and resource APIs do not depend on this orchestration layer.
type runtimeComponent interface {
	Name() string
	Reconcile(context.Context, *bx.BitrixCluster, *runtimeInputs) (ComponentResult, error)
}

type componentStep struct {
	name string
	run  func(context.Context, *bx.BitrixCluster, *runtimeInputs) error
}

func (s componentStep) Name() string { return s.name }
func (s componentStep) Reconcile(ctx context.Context, c *bx.BitrixCluster, in *runtimeInputs) (ComponentResult, error) {
	return captureComponentResult(s.name, c, func(snapshot *bx.BitrixCluster) error {
		return s.run(ctx, snapshot, in)
	})
}

// runRuntimeComponents adapts the existing isolated components to ReconcileStep.
// Existing stage ordering and short-circuit semantics remain unchanged.
func runRuntimeComponents(ctx context.Context, c *bx.BitrixCluster, in *runtimeInputs, components []runtimeComponent) ([]ComponentResult, error) {
	steps := make([]ReconcileStep, 0, len(components))
	for _, component := range components {
		steps = append(steps, ReconcileStep{
			Name: component.Name(),
			Run: func(ctx context.Context) (ComponentResult, StepResult, error) {
				observation, err := component.Reconcile(ctx, c, in)
				return observation, StepContinue(), err
			},
		})
	}
	outcome, err := RunReconcileSteps(ctx, &StatusManager{cluster: c}, steps)
	return outcome.Results, err
}

func (r *BitrixClusterReconciler) reconcileResources(ctx context.Context, c *bx.BitrixCluster) error {
	var in *runtimeInputs
	preparation, err := captureComponentResult("prepare", c, func(snapshot *bx.BitrixCluster) error {
		var prepErr error
		in, prepErr = r.prepareRuntime(ctx, snapshot)
		return prepErr
	})
	(&StatusManager{cluster: c}).Apply(preparation)
	if err != nil {
		return &componentError{name: "prepare", cause: err}
	}
	_, err = runRuntimeComponents(ctx, c, in, r.runtimeComponents())
	return err
}

// Preparation deliberately handles disabling old controllers before examining
// broken Secrets/configuration: this keeps cleanup and SSA transitions safe.
func (r *BitrixClusterReconciler) prepareRuntime(ctx context.Context, c *bx.BitrixCluster) (*runtimeInputs, error) {
	if err := r.deleteDisabledRuntime(ctx, c); err != nil {
		return nil, err
	}
	if err := validateWorkload(c); err != nil {
		return nil, err
	}
	port, err := r.publicationPort(ctx, c)
	if err != nil {
		return nil, err
	}
	bundle, err := render.RenderWithPort(c.Spec, port, workload.PushEndpoints(c))
	if err != nil {
		return nil, block("InvalidRuntimeConfiguration", err.Error())
	}
	image, err := render.ReleaseReference(c.Spec.Release.Image)
	if err != nil {
		return nil, block("InvalidRelease", err.Error())
	}
	revision, err := r.secrets(ctx, c, bundle)
	if err != nil {
		return nil, err
	}
	pvc, err := r.resourceManager().Storage(ctx, c)
	if err != nil {
		return nil, err
	}
	bound := pvc.Status.Phase == corev1.ClaimBound
	condition(c, "StorageReady", metav1.ConditionFalse, "VolumePending", "Waiting for the uploads PVC to bind")
	if bound {
		condition(c, "StorageReady", metav1.ConditionTrue, "VolumeBound", "Uploads PVC is bound")
	}
	return &runtimeInputs{
		desired:  runtimeDesiredState{bundle: bundle, revision: revision, image: image},
		observed: runtimeObservedState{storageBound: bound},
	}, nil
}

func (r *BitrixClusterReconciler) runtimeComponents() []runtimeComponent {
	return []runtimeComponent{
		componentStep{name: "config", run: r.reconcileRuntimeConfig},
		componentStep{name: stagePush, run: r.reconcileRuntimePush},
		componentStep{name: stageWeb, run: r.reconcileRuntimeWeb},
		componentStep{name: stageCron, run: r.reconcileRuntimeCron},
		componentStep{name: stageWebStatus, run: r.observeRuntimeWeb},
		componentStep{name: stageSites, run: r.reconcileRuntimeSites},
		componentStep{name: "config-gc", run: r.reconcileRuntimeConfigGC},
	}
}

func (r *BitrixClusterReconciler) deleteDisabledRuntime(ctx context.Context, c *bx.BitrixCluster) error {
	if !workload.AutoscalingEnabled(c) {
		if err := r.deleteHPA(ctx, c); err != nil {
			return err
		}
		condition(c, "AutoscalingReady", metav1.ConditionTrue, "Disabled", "Web autoscaling is disabled")
	}
	if !c.Spec.Runtime.Cron.Enabled {
		if err := r.deleteCron(ctx, c); err != nil {
			return err
		}
		c.Status.Runtime.Cron = bx.WorkloadStatus{}
		condition(c, conditionCronReady, metav1.ConditionTrue, "Disabled", "Cron workload is disabled")
	}
	if !pushEnabled(c) {
		if err := r.deletePush(ctx, c); err != nil {
			return err
		}
		c.Status.Runtime.PushPublisher = bx.WorkloadStatus{}
		c.Status.Runtime.PushSubscriber = bx.WorkloadStatus{}
		condition(c, conditionPushReady, metav1.ConditionTrue, "Disabled", "Push workloads are disabled")
	}
	return nil
}

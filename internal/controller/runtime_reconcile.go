package controller

import (
	"context"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	"github.com/m8-team/bitrix-operator/internal/workload"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// runtimeInputs are produced once, then shared read-only between the runtime
// components. Only the observed web readiness is filled in by the web stage.
type runtimeInputs struct {
	bundle   *render.Bundle
	revision string
	image    string
	pvcBound bool
	webReady bool
}

// runtimeComponent is the narrow interface for one independently testable
// reconciliation step. It is intentionally local to the controller: rendering
// and resource APIs do not depend on this orchestration layer.
type runtimeComponent interface {
	Name() string
	Reconcile(context.Context, *bx.BitrixCluster, *runtimeInputs) error
}

type componentStep struct {
	name string
	run  func(context.Context, *bx.BitrixCluster, *runtimeInputs) error
}

func (s componentStep) Name() string { return s.name }
func (s componentStep) Reconcile(ctx context.Context, c *bx.BitrixCluster, in *runtimeInputs) error {
	return s.run(ctx, c, in)
}

// runRuntimeComponents stops on the first error; later components must not
// observe an incomplete previous stage. This is also used by pure unit tests.
func runRuntimeComponents(ctx context.Context, c *bx.BitrixCluster, in *runtimeInputs, components []runtimeComponent) error {
	for _, component := range components {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := component.Reconcile(ctx, c, in); err != nil {
			return err
		}
	}
	return nil
}

func (r *BitrixClusterReconciler) reconcileResources(ctx context.Context, c *bx.BitrixCluster) error {
	in, err := r.prepareRuntime(ctx, c)
	if err != nil {
		return err
	}
	return runRuntimeComponents(ctx, c, in, r.runtimeComponents())
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
	bundle, err := render.RenderWithPort(c.Spec, r.publicationPort(ctx, c), workload.PushEndpoints(c))
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
	pvc, err := r.storage(ctx, c)
	if err != nil {
		return nil, err
	}
	bound := pvc.Status.Phase == corev1.ClaimBound
	condition(c, "StorageReady", metav1.ConditionFalse, "VolumePending", "Waiting for the uploads PVC to bind")
	if bound {
		condition(c, "StorageReady", metav1.ConditionTrue, "VolumeBound", "Uploads PVC is bound")
	}
	return &runtimeInputs{bundle: bundle, revision: revision, image: image, pvcBound: bound}, nil
}

func (r *BitrixClusterReconciler) runtimeComponents() []runtimeComponent {
	return []runtimeComponent{
		componentStep{name: "config", run: r.reconcileRuntimeConfig},
		componentStep{name: "push", run: r.reconcileRuntimePush},
		componentStep{name: "web", run: r.reconcileRuntimeWeb},
		componentStep{name: "cron", run: r.reconcileRuntimeCron},
		componentStep{name: "web-status", run: r.observeRuntimeWeb},
		componentStep{name: "sites", run: r.reconcileRuntimeSites},
		componentStep{name: "config-gc", run: r.reconcileRuntimeConfigGC},
	}
}

func (r *BitrixClusterReconciler) deleteDisabledRuntime(ctx context.Context, c *bx.BitrixCluster) error {
	if !workload.AutoscalingEnabled(c) {
		if err := r.deleteHPA(ctx, c); err != nil {
			return err
		}
	}
	if !c.Spec.Runtime.Cron.Enabled {
		if err := r.deleteCron(ctx, c); err != nil {
			return err
		}
	}
	if !pushEnabled(c) {
		if err := r.deletePush(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

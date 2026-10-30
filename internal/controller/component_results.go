package controller

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ComponentResult is the observed status delta produced by a reconciliation
// stage. Component implementations work on an isolated snapshot; only the
// orchestration/status layer can apply their observations to the shared CR.
type ComponentResult struct {
	// Applied is true when the stage completed applying or checking its desired
	// objects without error. It is NOT evidence of workload readiness: observed
	// health is carried exclusively in Conditions and workload status.
	Applied        bool
	Name           string
	Conditions     []metav1.Condition
	Runtime        *bx.RuntimeStatus
	Release        *bx.ReleaseStatus
	Sites          []bx.BitrixSiteStatus
	SiteValidation *bx.SiteValidationStatus

	RuntimeChanged        bool
	ReleaseChanged        bool
	SitesChanged          bool
	SiteValidationChanged bool
}

// SetCondition builds a stage-local observation; StatusManager alone merges
// it into the accumulated CR status, with transition timestamps managed there.
func (result *ComponentResult) SetCondition(generation int64, kind string, state metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&result.Conditions, metav1.Condition{
		Type: kind, Status: state, Reason: reason, Message: message,
		ObservedGeneration: generation,
	})
}

// RuntimeFor returns a private copy of the previously observed workload state.
// It prevents one stage from resetting other stages' replica observations.
func (result *ComponentResult) RuntimeFor(previous *bx.RuntimeStatus) *bx.RuntimeStatus {
	if !result.RuntimeChanged {
		result.Runtime = &bx.RuntimeStatus{}
		if previous != nil {
			result.Runtime = previous.DeepCopy()
		}
		result.RuntimeChanged = true
	}
	return result.Runtime
}

// captureComponentResult isolates older component implementations that still
// use condition(c, ...) internally. They can be migrated to construct results
// directly without changing orchestration or their Kubernetes operations.
func captureComponentResult(name string, cluster *bx.BitrixCluster, reconcile func(*bx.BitrixCluster) error) (ComponentResult, error) {
	snapshot := cluster.DeepCopy()
	start := time.Now()
	err := reconcile(snapshot)
	recordStageMetrics(name, err, time.Since(start))
	result := ComponentResult{Name: name, Applied: err == nil}
	for _, after := range snapshot.Status.Conditions {
		before := meta.FindStatusCondition(cluster.Status.Conditions, after.Type)
		if before == nil || !reflect.DeepEqual(*before, after) {
			result.Conditions = append(result.Conditions, after)
		}
	}
	if !reflect.DeepEqual(cluster.Status.Runtime, snapshot.Status.Runtime) {
		result.RuntimeChanged = true
		result.Runtime = snapshot.Status.Runtime
	}
	if !reflect.DeepEqual(cluster.Status.Release, snapshot.Status.Release) {
		result.ReleaseChanged = true
		result.Release = snapshot.Status.Release
	}
	if !reflect.DeepEqual(cluster.Status.Sites, snapshot.Status.Sites) {
		result.SitesChanged = true
		result.Sites = snapshot.Status.Sites
	}
	if !reflect.DeepEqual(cluster.Status.SiteValidation, snapshot.Status.SiteValidation) {
		result.SiteValidationChanged = true
		result.SiteValidation = snapshot.Status.SiteValidation
	}
	return result, err
}

type componentError struct {
	name  string
	cause error
}

func (e *componentError) Error() string { return fmt.Sprintf("%s: %v", e.name, e.cause) }
func (e *componentError) Unwrap() error { return e.cause }

func failedComponent(err error, name string) bool {
	var failure *componentError
	return errors.As(err, &failure) && failure.name == name
}

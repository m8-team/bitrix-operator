package controller

import (
	"errors"
	"fmt"
	"reflect"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ComponentResult is the observed status delta produced by a reconciliation
// stage. Component implementations work on an isolated snapshot; only the
// orchestration/status layer can apply their observations to the shared CR.
type ComponentResult struct {
	Name       string
	Conditions []metav1.Condition
	Runtime    *bx.RuntimeStatus
	Release    *bx.ReleaseStatus
	Sites      []bx.BitrixSiteStatus

	RuntimeChanged bool
	ReleaseChanged bool
	SitesChanged   bool
}

// captureComponentResult isolates older component implementations that still
// use condition(c, ...) internally. They can be migrated to construct results
// directly without changing orchestration or their Kubernetes operations.
func captureComponentResult(name string, cluster *bx.BitrixCluster, reconcile func(*bx.BitrixCluster) error) (ComponentResult, error) {
	snapshot := cluster.DeepCopy()
	err := reconcile(snapshot)
	result := ComponentResult{Name: name}
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
	return result, err
}

// applyComponentResult is the only way a runtime/publication stage changes the
// accumulated status. Importantly, a stage cannot reset unrelated Conditions.
func applyComponentResult(cluster *bx.BitrixCluster, result ComponentResult) {
	for _, condition := range result.Conditions {
		meta.SetStatusCondition(&cluster.Status.Conditions, condition)
	}
	if result.RuntimeChanged {
		cluster.Status.Runtime = result.Runtime
	}
	if result.ReleaseChanged {
		cluster.Status.Release = result.Release
	}
	if result.SitesChanged {
		cluster.Status.Sites = result.Sites
	}
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

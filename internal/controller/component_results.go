package controller

import (
	"errors"
	"fmt"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ComponentResult is the observed status delta produced by a reconciliation
// stage. Components emit isolated, explicitly scoped results; only the
// StatusManager can merge their observations into the shared CR.
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

// SitesFor returns a private copy of site status, including per-site URLs.
// StatusManager merges this isolated result only after the stage completes.
func (result *ComponentResult) SitesFor(previous []bx.BitrixSiteStatus) []bx.BitrixSiteStatus {
	if !result.SitesChanged {
		result.Sites = make([]bx.BitrixSiteStatus, len(previous))
		for i := range previous {
			result.Sites[i] = *previous[i].DeepCopy()
		}
		result.SitesChanged = true
	}
	return result.Sites
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

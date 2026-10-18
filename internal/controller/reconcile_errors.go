package controller

import (
	"errors"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// reconcileFailure separates a user-actionable block, a concurrent mutation,
// and a transient API failure. Only the latter goes to controller-runtime's
// rate-limited retry queue. Conditions never expose raw API error bodies.
type reconcileFailure struct {
	reason     string
	message    string
	retryAfter time.Duration
	cause      error
}

func classifyReconcileError(err error, publication bool) reconcileFailure {
	if issue, ok := errors.AsType[*blocked](err); ok {
		return reconcileFailure{reason: issue.reason, message: issue.message, retryAfter: time.Minute}
	}
	if apierrors.IsConflict(err) || apierrors.IsAlreadyExists(err) {
		reason, message := "ApplyConflict", "An application resource changed concurrently"
		if publication {
			reason, message = "ResourceConflict", "A Gateway publication resource changed concurrently"
		}
		return reconcileFailure{reason: reason, message: message, retryAfter: 10 * time.Second}
	}
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
		return reconcileFailure{reason: "PermissionDenied", message: "The operator lacks permission to reconcile a Kubernetes resource", cause: err}
	}
	if apierrors.IsNotFound(err) {
		return reconcileFailure{reason: "DependencyMissing", message: "A required Kubernetes resource is not yet available", cause: err}
	}
	if apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || apierrors.IsTooManyRequests(err) || apierrors.IsServiceUnavailable(err) {
		return reconcileFailure{reason: "APIUnavailable", message: "Kubernetes API is temporarily unavailable; reconciliation will retry", cause: err}
	}
	if publication {
		return reconcileFailure{reason: "PublicationFailed", message: "Could not reconcile Gateway publication; see controller logs", cause: err}
	}
	return reconcileFailure{reason: "ReconcileFailed", message: "Could not reconcile application resources; see controller logs", cause: err}
}

func shorterRequeue(current, proposed time.Duration) time.Duration {
	if proposed > 0 && (current == 0 || proposed < current) {
		return proposed
	}
	return current
}

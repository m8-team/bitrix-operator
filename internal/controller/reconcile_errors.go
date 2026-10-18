package controller

import (
	"errors"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// The error policy distinguishes expected waiting, user-actionable blocks,
// and transient failures which controller-runtime retries with rate limiting.
type reconcileDisposition string

const (
	dispositionWaiting   reconcileDisposition = "Waiting"
	dispositionBlocked   reconcileDisposition = "Blocked"
	dispositionTransient reconcileDisposition = "Transient"
)

type waiting struct {
	reason, message string
	retryAfter      time.Duration
}

func (w *waiting) Error() string { return w.message }

func waitFor(reason, message string, after time.Duration) error {
	return &waiting{reason: reason, message: message, retryAfter: after}
}

type reconcileFailure struct {
	disposition reconcileDisposition
	reason      string
	message     string
	retryAfter  time.Duration
	cause       error
}

func classifyReconcileError(err error, publication bool) reconcileFailure {
	if err == nil {
		return reconcileFailure{disposition: dispositionWaiting, retryAfter: 30 * time.Second}
	}
	if issue, ok := errors.AsType[*waiting](err); ok {
		return reconcileFailure{disposition: dispositionWaiting, reason: issue.reason, message: issue.message, retryAfter: issue.retryAfter}
	}
	if issue, ok := errors.AsType[*blocked](err); ok {
		return reconcileFailure{disposition: dispositionBlocked, reason: issue.reason, message: issue.message, retryAfter: time.Minute}
	}
	if apierrors.IsConflict(err) || apierrors.IsAlreadyExists(err) {
		reason, message := "ApplyConflict", "An application resource changed concurrently"
		if publication {
			reason, message = "ResourceConflict", "A Gateway publication resource changed concurrently"
		}
		return reconcileFailure{disposition: dispositionWaiting, reason: reason, message: message, retryAfter: 10 * time.Second}
	}
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
		return reconcileFailure{disposition: dispositionTransient, reason: "PermissionDenied", message: "The operator lacks permission to reconcile a Kubernetes resource", cause: err}
	}
	if apierrors.IsNotFound(err) {
		return reconcileFailure{disposition: dispositionTransient, reason: "DependencyMissing", message: "A required Kubernetes resource is not yet available", cause: err}
	}
	if apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || apierrors.IsTooManyRequests(err) || apierrors.IsServiceUnavailable(err) {
		return reconcileFailure{disposition: dispositionTransient, reason: "APIUnavailable", message: "Kubernetes API is temporarily unavailable; reconciliation will retry", cause: err}
	}
	if publication {
		return reconcileFailure{disposition: dispositionTransient, reason: "PublicationFailed", message: "Could not reconcile Gateway publication; see controller logs", cause: err}
	}
	return reconcileFailure{disposition: dispositionTransient, reason: "ReconcileFailed", message: "Could not reconcile application resources; see controller logs", cause: err}
}

func shorterRequeue(current, proposed time.Duration) time.Duration {
	if proposed > 0 && (current == 0 || proposed < current) {
		return proposed
	}
	return current
}

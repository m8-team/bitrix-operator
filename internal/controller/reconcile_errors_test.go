package controller

import (
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestClassifyReconcileError(t *testing.T) {
	resource := schema.GroupResource{Group: "apps", Resource: "deployments"}
	cases := []struct {
		name          string
		err           error
		publication   bool
		reason        string
		retryAfter    time.Duration
		preserveError bool
	}{
		{name: "missing required Secret", err: block("SecretMissing", "secret absent"), reason: "SecretMissing", retryAfter: time.Minute},
		{name: "invalid configuration", err: block("InvalidRuntimeConfiguration", "bad config"), reason: "InvalidRuntimeConfiguration", retryAfter: time.Minute},
		{name: "conflicting apply", err: apierrors.NewConflict(resource, "web", errors.New("conflict")), reason: "ApplyConflict", retryAfter: 10 * time.Second},
		{name: "conflicting Gateway", err: apierrors.NewConflict(resource, "route", errors.New("conflict")), publication: true, reason: "ResourceConflict", retryAfter: 10 * time.Second},
		{name: "throttled API", err: apierrors.NewTooManyRequests("busy", 1), reason: "APIUnavailable", preserveError: true},
		{name: "missing Kubernetes dependency", err: apierrors.NewNotFound(resource, "web"), reason: "DependencyMissing", preserveError: true},
		{name: "unexpected failure", err: errors.New("transport failure"), reason: "ReconcileFailed", preserveError: true},
		{name: "publication failure", err: errors.New("transport failure"), publication: true, reason: "PublicationFailed", preserveError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyReconcileError(tc.err, tc.publication)
			if got.reason != tc.reason || got.retryAfter != tc.retryAfter {
				t.Fatalf("unexpected policy: got %#v, want reason %q and retry %s", got, tc.reason, tc.retryAfter)
			}
			if (got.cause != nil) != tc.preserveError {
				t.Fatalf("unexpected retry disposition for %q: cause=%v", tc.name, got.cause)
			}
			if got.message == "" {
				t.Fatal("missing user-visible condition message")
			}
		})
	}
}

func TestShorterRequeue(t *testing.T) {
	if got := shorterRequeue(30*time.Second, 10*time.Second); got != 10*time.Second {
		t.Fatalf("shorter requeue = %s", got)
	}
	if got := shorterRequeue(30*time.Second, time.Minute); got != 30*time.Second {
		t.Fatalf("longer requeue = %s", got)
	}
}

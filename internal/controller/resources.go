package controller

import "sigs.k8s.io/controller-runtime/pkg/client"

type blocked struct{ reason, message string }

func (e *blocked) Error() string         { return e.message }
func block(reason, message string) error { return &blocked{reason, message} }

// reader is uncached for ownership, Secret data, guarded mutation and read-after-write.
func (r *BitrixClusterReconciler) reader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

// Rollout observations use the event-driven informer cache.
func (r *BitrixClusterReconciler) observationReader() client.Reader { return r.Client }

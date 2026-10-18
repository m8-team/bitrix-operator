package controller

import (
	"time"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Stage names and outcomes are bounded enums, never BitrixCluster/site/Secret names.
// In particular, no error strings or credentials enter Prometheus labels.
var stageDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: "bitrix_operator",
	Name:      "reconcile_stage_duration_seconds",
	Help:      "Time spent in a reconciliation stage, classified by its disposition.",
	Buckets:   prometheus.DefBuckets,
}, []string{"stage", "outcome"})

var stageAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "bitrix_operator",
	Name:      "reconcile_stage_attempts_total",
	Help:      "Number of stage attempts by result disposition.",
}, []string{"stage", "outcome"})

// Reconcile outcomes provide an aggregate signal without resource names or
// unbounded reason/message labels. Stage metrics remain the detail drilldown.
var reconcileOutcome = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "bitrix_operator",
	Name:      "reconcile_outcomes_total",
	Help:      "Completed reconciliations by final result, with fixed label values.",
}, []string{"result"})

func init() {
	metrics.Registry.MustRegister(stageDuration, stageAttempts, reconcileOutcome)
}

func boundedReconcileResult(c *bx.BitrixCluster, err error) string {
	if err != nil {
		return "error"
	}
	switch c.Status.Phase {
	case bx.BitrixClusterPhaseReady:
		return "ready"
	case bx.BitrixClusterPhaseDegraded:
		return "degraded"
	case bx.BitrixClusterPhaseProgressing, bx.BitrixClusterPhasePending:
		return "progressing"
	default:
		return "other"
	}
}

func recordReconcileOutcome(c *bx.BitrixCluster, err error) {
	reconcileOutcome.WithLabelValues(boundedReconcileResult(c, err)).Inc()
}

func boundedStageName(name string) string {
	switch name {
	case "prepare", "config", "push", "web", "cron", "web-status", "sites", "config-gc", "gateway":
		return name
	default:
		return "other"
	}
}

func stageOutcome(name string, err error) string {
	if err == nil {
		return "success"
	}
	return string(classifyReconcileError(err, name == "gateway").disposition)
}

func recordStageMetrics(name string, err error, elapsed time.Duration) {
	labels := []string{boundedStageName(name), stageOutcome(name, err)}
	stageDuration.WithLabelValues(labels...).Observe(elapsed.Seconds())
	stageAttempts.WithLabelValues(labels...).Inc()
}

package controller

import (
	"errors"
	"testing"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestStageMetricLabelsAreBounded(t *testing.T) {
	for _, name := range []string{"prepare", "config", "push", "web", "cron", "web-status", "sites", "config-gc", "gateway"} {
		if boundedStageName(name) != name {
			t.Fatalf("registered stage %q was folded away", name)
		}
	}
	for _, name := range []string{"secret/customer-name", "site:example.com", "untrusted-key"} {
		if boundedStageName(name) != "other" {
			t.Fatalf("untrusted stage %q became a label", name)
		}
	}
}

func TestStageMetricDispositions(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"config", nil, "success"},
		{"prepare", waitFor("CronStopping", "waiting", 30*time.Second), "Waiting"},
		{"gateway", block("InvalidGateway", "invalid"), "Blocked"},
		{"web", errors.New("API transport unavailable"), "Transient"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if outcome := stageOutcome(tc.name, tc.err); outcome != tc.want {
				t.Fatalf("stage outcome = %q; want %q", outcome, tc.want)
			}
			counter := stageAttempts.WithLabelValues(tc.name, tc.want)
			before := testutil.ToFloat64(counter)
			recordStageMetrics(tc.name, tc.err, 20*time.Millisecond)
			if got := testutil.ToFloat64(counter); got != before+1 {
				t.Fatalf("stage attempts = %v; want %v", got, before+1)
			}
		})
	}
}

func TestReconcileOutcomeBoundedAndCounted(t *testing.T) {
	for _, test := range []struct {
		phase bx.BitrixClusterPhase
		err   error
		want  string
	}{
		{bx.BitrixClusterPhaseReady, nil, "ready"},
		{bx.BitrixClusterPhaseDegraded, nil, "degraded"},
		{bx.BitrixClusterPhaseProgressing, nil, "progressing"},
		{bx.BitrixClusterPhasePending, nil, "progressing"},
		{bx.BitrixClusterPhaseReady, errors.New("sensitive customer error"), "error"},
		{"unexpected-tenant-secret", nil, "other"},
	} {
		cluster := &bx.BitrixCluster{Status: bx.BitrixClusterStatus{Phase: test.phase}}
		if got := boundedReconcileResult(cluster, test.err); got != test.want {
			t.Fatalf("result=%q, expected %q", got, test.want)
		}
		counter := reconcileOutcome.WithLabelValues(test.want)
		before := testutil.ToFloat64(counter)
		recordReconcileOutcome(cluster, test.err)
		if got := testutil.ToFloat64(counter); got != before+1 {
			t.Fatalf("count=%v, expected %v", got, before+1)
		}
	}
}

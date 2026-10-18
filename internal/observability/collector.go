// Package observability exposes bounded, non-sensitive operator state.
package observability

import (
	"context"
	"time"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var conditionTypes = []string{"Ready", "StorageReady", "WebReady", "CronReady", "PushReady", "SitesValidated", "GatewayReady", "AutoscalingReady"}

const clusterLabel = "cluster"
const namespaceLabel = "cluster_namespace"

// Collector reads a cache snapshot per scrape, including on manager followers.
// It retains no series after deletion and needs no reconcile to recover on restart.
type Collector struct {
	reader                                                       client.Reader
	success, duration, clusterCount, unreadyCount, generation, observed, condition, replicas, siteValidationTimestamp *prometheus.Desc
}

func NewCollector(reader client.Reader) *Collector {
	labels := []string{namespaceLabel, clusterLabel}
	return &Collector{
		reader:     reader,
		success:    prometheus.NewDesc("bitrix_operator_collection_success", "Whether the cluster cache was read successfully (1 or 0).", nil, nil),
		duration:   prometheus.NewDesc("bitrix_operator_collection_duration_seconds", "Time spent collecting the cluster cache snapshot.", nil, nil),
		clusterCount: prometheus.NewDesc("bitrix_operator_collection_clusters", "Number of active BitrixCluster resources seen in the cache snapshot.", nil, nil),
		unreadyCount: prometheus.NewDesc("bitrix_operator_collection_unready_clusters", "Number of resources not confirmed Ready for the latest generation.", nil, nil),
		siteValidationTimestamp: prometheus.NewDesc("bitrix_cluster_site_validation_last_success_timestamp_seconds", "Last persisted successful Bitrix site validation timestamp, regardless of current readiness.", labels, nil),
		generation: prometheus.NewDesc("bitrix_cluster_generation", "Current desired BitrixCluster generation.", labels, nil),
		observed:   prometheus.NewDesc("bitrix_cluster_observed_generation", "Last persisted observed BitrixCluster generation.", labels, nil),
		condition:  prometheus.NewDesc("bitrix_cluster_condition", "One-hot condition status; missing or stale conditions are Unknown.", []string{namespaceLabel, clusterLabel, "condition", "status"}, nil),
		replicas:   prometheus.NewDesc("bitrix_cluster_replicas", "Last persisted workload replica count; consult observed generation for freshness.", []string{namespaceLabel, clusterLabel, "component", "state"}, nil),
	}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{c.success, c.duration, c.clusterCount, c.unreadyCount, c.generation, c.observed, c.condition, c.replicas, c.siteValidationTimestamp} {
		ch <- desc
	}
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	start := time.Now()
	defer func() {
		ch <- prometheus.MustNewConstMetric(c.duration, prometheus.GaugeValue, time.Since(start).Seconds())
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	list := &bx.BitrixClusterList{}
	if err := c.reader.List(ctx, list); err != nil {
		// Do not expose client errors or present failed collection as healthy zeroes.
		ch <- prometheus.MustNewConstMetric(c.success, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.success, prometheus.GaugeValue, 1)
	count, unready := 0, 0
	for i := range list.Items {
		cluster := &list.Items[i]
		if !cluster.DeletionTimestamp.IsZero() {
			continue
		}
		count++
		if cluster.Status.ObservedGeneration != cluster.Generation {
			unready++
		} else {
			ready := meta.FindStatusCondition(cluster.Status.Conditions, "Ready")
			if ready == nil || ready.Status != metav1.ConditionTrue || ready.ObservedGeneration != cluster.Generation {
				unready++
			}
		}
		c.collectCluster(ch, cluster)
	}
	ch <- prometheus.MustNewConstMetric(c.clusterCount, prometheus.GaugeValue, float64(count))
	ch <- prometheus.MustNewConstMetric(c.unreadyCount, prometheus.GaugeValue, float64(unready))
}

func (c *Collector) collectCluster(ch chan<- prometheus.Metric, cluster *bx.BitrixCluster) {
	ch <- prometheus.MustNewConstMetric(c.generation, prometheus.GaugeValue, float64(cluster.Generation), cluster.Namespace, cluster.Name)
	ch <- prometheus.MustNewConstMetric(c.observed, prometheus.GaugeValue, float64(cluster.Status.ObservedGeneration), cluster.Namespace, cluster.Name)
	if proof := cluster.Status.SiteValidation; proof != nil && !proof.ValidatedAt.IsZero() {
		ch <- prometheus.MustNewConstMetric(c.siteValidationTimestamp, prometheus.GaugeValue, float64(proof.ValidatedAt.Unix()), cluster.Namespace, cluster.Name)
	}
	for _, kind := range conditionTypes {
		status := metav1.ConditionUnknown
		condition := meta.FindStatusCondition(cluster.Status.Conditions, kind)
		if condition != nil && condition.ObservedGeneration == cluster.Generation && cluster.Status.ObservedGeneration == cluster.Generation {
			switch condition.Status {
			case metav1.ConditionTrue, metav1.ConditionFalse:
				status = condition.Status
			}
		}
		for _, value := range []metav1.ConditionStatus{metav1.ConditionTrue, metav1.ConditionFalse, metav1.ConditionUnknown} {
			active := float64(0)
			if status == value {
				active = 1
			}
			ch <- prometheus.MustNewConstMetric(c.condition, prometheus.GaugeValue, active, cluster.Namespace, cluster.Name, kind, string(value))
		}
	}
	if cluster.Status.Runtime == nil {
		return
	}
	runtime := cluster.Status.Runtime
	for component, status := range map[string]bx.WorkloadStatus{
		"web": runtime.Web, "cron": runtime.Cron, "push_publisher": runtime.PushPublisher, "push_subscriber": runtime.PushSubscriber,
	} {
		for state, value := range map[string]int32{"desired": status.DesiredReplicas, "ready": status.ReadyReplicas, "updated": status.UpdatedReplicas, "available": status.AvailableReplicas} {
			ch <- prometheus.MustNewConstMetric(c.replicas, prometheus.GaugeValue, float64(value), cluster.Namespace, cluster.Name, component, state)
		}
	}
}

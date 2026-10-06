package workload

import (
	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	meta "k8s.io/client-go/applyconfigurations/meta/v1"
	policy "k8s.io/client-go/applyconfigurations/policy/v1"
)

// DisruptionBudget follows the Deployment's current scale, including HPA changes.
// A singleton can be evicted; availability during maintenance requires >=2 replicas.
func DisruptionBudget(c *bx.BitrixCluster) *policy.PodDisruptionBudgetApplyConfiguration {
	return policy.PodDisruptionBudget(Name(c, "web"), c.Namespace).
		WithLabels(labels(c)).WithOwnerReferences(owner(c)).WithSpec(
		policy.PodDisruptionBudgetSpec().WithMaxUnavailable(intstr.FromInt32(1)).
			WithUnhealthyPodEvictionPolicy(policyv1.AlwaysAllow).
			WithSelector(meta.LabelSelector().WithMatchLabels(labels(c))))
}

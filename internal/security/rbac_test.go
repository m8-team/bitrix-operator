package security_test

import (
	"os"
	"slices"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"
)

func TestManagerRoleHasNoPrivilegeExpansion(t *testing.T) {
	data, err := os.ReadFile("../../config/rbac/role.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var role rbacv1.ClusterRole
	if err := yaml.UnmarshalStrict(data, &role); err != nil {
		t.Fatal(err)
	}
	if len(role.Rules) == 0 {
		t.Fatal("empty generated role")
	}
	for _, rule := range role.Rules {
		if slices.Contains(rule.APIGroups, "*") || slices.Contains(rule.Resources, "*") || slices.Contains(rule.Verbs, "*") {
			t.Fatal("wildcard permission requires new security review")
		}
		for _, verb := range rule.Verbs {
			if slices.Contains([]string{"bind", "escalate", "impersonate"}, verb) {
				t.Fatal("privilege escalation permission")
			}
		}
		for _, resource := range rule.Resources {
			if slices.Contains([]string{"pods/exec", "pods/attach", "pods/portforward", "serviceaccounts/token",
				"roles", "rolebindings", "clusterroles", "clusterrolebindings"}, resource) {
				t.Fatalf("unexpected administrative permission: %s", resource)
			}
			if resource == "secrets" {
				for _, verb := range rule.Verbs {
					if !slices.Contains([]string{"get", "list", "watch"}, verb) {
						t.Fatal("operator must not mutate external Secrets")
					}
				}
			}
		}
	}
}

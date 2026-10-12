package security_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"testing"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"github.com/m8-team/bitrix-operator/internal/workload"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/yaml"
)

type endpoint struct {
	namespace string
	labels    map[string]string
}

func renderPolicies(t *testing.T, profile string) []networkingv1.NetworkPolicy {
	t.Helper()
	data, err := exec.Command("../../bin/kustomize", "build", "../../config/security/"+profile).CombinedOutput()
	if err != nil {
		t.Fatalf("Kustomize %s: %v: %s", profile, err, data)
	}
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	var policies []networkingv1.NetworkPolicy
	for {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		var header metav1.TypeMeta
		if err := json.Unmarshal(raw, &header); err != nil {
			t.Fatal(err)
		}
		if header.Kind != "NetworkPolicy" {
			continue
		}
		var policy networkingv1.NetworkPolicy
		strict := json.NewDecoder(bytes.NewReader(raw))
		strict.DisallowUnknownFields()
		if err := strict.Decode(&policy); err != nil {
			t.Fatal(err)
		}
		policies = append(policies, policy)
	}
	if len(policies) == 0 {
		t.Fatal("profile has no policies")
	}
	return policies
}

func TestWorkloadTrafficContract(t *testing.T) {
	for _, profile := range []string{"workloads", "postgresql"} {
		t.Run(profile, func(t *testing.T) {
			policies := renderPolicies(t, profile)
			for _, uid := range []string{"tenant-a", "tenant-b"} {
				c := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Name: uid, Namespace: "bitrix-sites", UID: types.UID(uid)}}
				for _, role := range []string{"publisher", "subscriber"} {
					data, err := json.Marshal(workload.PushPolicy(c, role))
					if err != nil {
						t.Fatal(err)
					}
					var p networkingv1.NetworkPolicy
					if err := json.Unmarshal(data, &p); err != nil {
						t.Fatal(err)
					}
					policies = append(policies, p)
				}
			}
			dbPort, wrongDBPort := int32(3306), int32(5432)
			if profile == "postgresql" {
				dbPort, wrongDBPort = wrongDBPort, dbPort
			}
			points := map[string]endpoint{
				"web": app("bitrix"), "cron": app("bitrix-cron"), "sites": app("bitrix-sites"),
				"publisher": app("bitrix-push-publisher"), "subscriber": app("bitrix-push-subscriber"),
				"db":                {"bitrix-data", map[string]string{"app.kubernetes.io/name": "bitrix-database"}},
				"redis":             {"bitrix-data", map[string]string{"app.kubernetes.io/name": "bitrix-redis"}},
				"dns":               {"kube-system", map[string]string{"k8s-app": "kube-dns"}},
				"gateway":           {"gateway-system", map[string]string{"bitrix.m8.team/traffic-role": "gateway"}},
				"wrong-ns-gateway":  {"other", map[string]string{"bitrix.m8.team/traffic-role": "gateway"}},
				"wrong-pod-gateway": {"gateway-system", map[string]string{"app": "other"}},
				"rogue":             {"bitrix-sites", map[string]string{"app": "other"}},
				"internet":          {"outside", nil},
				"other-publisher":   {"bitrix-sites", map[string]string{"app.kubernetes.io/name": "bitrix-push-publisher", workload.OwnerUID: "tenant-b"}},
				"wrong-ns-db":       {"other", map[string]string{"app.kubernetes.io/name": "bitrix-database"}},
				"wrong-pod-db":      {"bitrix-data", map[string]string{"app": "other"}},
			}
			cases := []struct {
				from, to string
				port     int32
				allow    bool
			}{
				{"gateway", "web", 8080, true}, {"wrong-ns-gateway", "web", 8080, false},
				{"wrong-pod-gateway", "web", 8080, false}, {"gateway", "web", 8081, false},
				{"gateway", "publisher", 9010, false}, {"gateway", "subscriber", 8010, false},
				{"rogue", "web", 8080, false}, {"web", "web", 8080, false},
				{"rogue", "cron", 8081, false}, {"rogue", "sites", 8081, false},
				{"web", "db", dbPort, true}, {"cron", "db", dbPort, true}, {"sites", "db", dbPort, true},
				{"publisher", "db", dbPort, false}, {"subscriber", "db", dbPort, false},
				{"web", "db", wrongDBPort, false}, {"web", "wrong-ns-db", dbPort, false},
				{"web", "wrong-pod-db", dbPort, false}, {"rogue", "db", dbPort, false},
				{"web", "publisher", 9010, true}, {"web", "subscriber", 8010, true},
				{"cron", "publisher", 9010, true}, {"cron", "subscriber", 8010, false},
				{"sites", "publisher", 9010, false}, {"web", "other-publisher", 9010, false},
				{"web", "subscriber", 9010, false}, {"web", "internet", 443, false},
			}
			for _, tc := range cases {
				t.Run(fmt.Sprintf("%s/%s/%d", tc.from, tc.to, tc.port), func(t *testing.T) {
					checkFlow(t, policies, points[tc.from], points[tc.to], tc.port, corev1.ProtocolTCP, tc.allow)
				})
			}
			for _, role := range []string{"web", "cron", "sites", "publisher", "subscriber"} {
				checkFlow(t, policies, points[role], points["redis"], 6379, corev1.ProtocolTCP, true)
				checkFlow(t, policies, points[role], points["internet"], 443, corev1.ProtocolTCP, false)
				for _, protocol := range []corev1.Protocol{corev1.ProtocolTCP, corev1.ProtocolUDP} {
					checkFlow(t, policies, points[role], points["dns"], 53, protocol, true)
					checkFlow(t, policies, points[role], points["dns"], 54, protocol, false)
				}
			}
		})
	}
}

func app(name string) endpoint {
	return endpoint{"bitrix-sites", map[string]string{"app.kubernetes.io/name": name, workload.OwnerUID: "tenant-a"}}
}

func TestManagerTrafficContract(t *testing.T) {
	policies := renderPolicies(t, "manager")
	manager := endpoint{"bitrix-operator-system", map[string]string{"app.kubernetes.io/name": "bitrix-operator", "control-plane": "controller-manager"}}
	for _, tc := range []struct {
		namespace, name string
		port            int32
		allow           bool
	}{
		{"monitoring", "prometheus", 8443, true}, {"other", "prometheus", 8443, false},
		{"monitoring", "other", 8443, false}, {"monitoring", "prometheus", 8081, false},
	} {
		peer := endpoint{tc.namespace, map[string]string{"app.kubernetes.io/name": tc.name}}
		checkFlow(t, policies, peer, manager, tc.port, corev1.ProtocolTCP, tc.allow)
	}
	// Manager egress is deliberately not isolated until real API endpoints are known.
	checkFlow(t, policies, manager, endpoint{"api-server", nil}, 6443, corev1.ProtocolTCP, true)
}

// Selector/port contract only, not CNI enforcement, NAT, node/loopback traffic,
// existing connections, DNS resolution or authentication. Endpoints represent distinct Pods.
func checkFlow(t *testing.T, policies []networkingv1.NetworkPolicy, from, to endpoint, port int32, protocol corev1.Protocol, want bool) {
	t.Helper()
	got := allowed(t, policies, from, to, port, protocol, networkingv1.PolicyTypeEgress) &&
		allowed(t, policies, to, from, port, protocol, networkingv1.PolicyTypeIngress)
	if got != want {
		t.Fatalf("%v -> %v %s/%d: allowed=%v, want %v", from, to, protocol, port, got, want)
	}
}

func allowed(t *testing.T, policies []networkingv1.NetworkPolicy, local, remote endpoint, port int32, protocol corev1.Protocol, direction networkingv1.PolicyType) bool {
	t.Helper()
	isolated := false
	for _, p := range policies {
		if p.Namespace != local.namespace || !matches(t, &p.Spec.PodSelector, local.labels) || !slices.Contains(p.Spec.PolicyTypes, direction) {
			continue
		}
		isolated = true
		if direction == networkingv1.PolicyTypeIngress {
			for _, rule := range p.Spec.Ingress {
				if portsMatch(t, rule.Ports, port, protocol) && peersMatch(t, rule.From, p.Namespace, remote) {
					return true
				}
			}
		} else {
			for _, rule := range p.Spec.Egress {
				if portsMatch(t, rule.Ports, port, protocol) && peersMatch(t, rule.To, p.Namespace, remote) {
					return true
				}
			}
		}
	}
	return !isolated
}

func matches(t *testing.T, selector *metav1.LabelSelector, values map[string]string) bool {
	t.Helper()
	s, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		t.Fatal(err)
	}
	return s.Matches(labels.Set(values))
}

func portsMatch(t *testing.T, ports []networkingv1.NetworkPolicyPort, port int32, protocol corev1.Protocol) bool {
	t.Helper()
	for _, p := range ports {
		if p.Port != nil && p.Port.Type != intstr.Int || p.EndPort != nil {
			t.Fatal("extend evaluator before using named ports or ranges")
		}
		proto := corev1.ProtocolTCP
		if p.Protocol != nil {
			proto = *p.Protocol
		}
		if proto == protocol && (p.Port == nil || p.Port.IntVal == port) {
			return true
		}
	}
	return len(ports) == 0
}

func peersMatch(t *testing.T, peers []networkingv1.NetworkPolicyPeer, namespace string, remote endpoint) bool {
	t.Helper()
	for _, p := range peers {
		if p.IPBlock != nil {
			t.Fatal("extend evaluator before using IPBlock")
		}
		namespaceMatches := p.NamespaceSelector == nil && (p.PodSelector == nil || remote.namespace == namespace)
		if p.NamespaceSelector != nil {
			namespaceMatches = matches(t, p.NamespaceSelector, map[string]string{"kubernetes.io/metadata.name": remote.namespace})
		}
		if namespaceMatches && (p.PodSelector == nil || matches(t, p.PodSelector, remote.labels)) {
			return true
		}
	}
	return len(peers) == 0
}

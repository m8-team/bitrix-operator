package controller

import (
	"testing"

	gateway "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	overlapHost = "portal.example.com"
	overlapWildcard = "*.example.com"
)

func TestRouteOverlapsHostnames(t *testing.T) {
	tests := []struct {
		name    string
		current []gateway.Hostname
		pending []gateway.Hostname
		want    bool
	}{
		{name: "same hostname", current: []gateway.Hostname{overlapHost}, pending: []gateway.Hostname{overlapHost}, want: true},
		{name: "removed hostname", current: []gateway.Hostname{"old.example.com"}, pending: []gateway.Hostname{"new.example.com"}},
		{name: "wildcard old route", current: []gateway.Hostname{overlapWildcard}, pending: []gateway.Hostname{overlapHost}, want: true},
		{name: "wildcard new route", current: []gateway.Hostname{overlapHost}, pending: []gateway.Hostname{overlapWildcard}, want: true},
		{name: "different domains", current: []gateway.Hostname{overlapWildcard}, pending: []gateway.Hostname{"portal.example.net"}},
		{name: "old route matching all hosts", pending: []gateway.Hostname{overlapHost}, want: true},
		{name: "no desired replacement hosts", current: []gateway.Hostname{overlapHost}},
		{name: "empty prior and desired", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			route := &gateway.HTTPRoute{
				Spec: gateway.HTTPRouteSpec{Hostnames: tc.current},
			}
			if got := routeOverlapsHostnames(route, tc.pending); got != tc.want {
				t.Fatalf("overlap=%t, want %t (old=%v, pending=%v)", got, tc.want, tc.current, tc.pending)
			}
		})
	}
}

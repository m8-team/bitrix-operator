package controller

import (
	"testing"

	gateway "sigs.k8s.io/gateway-api/apis/v1"
)

func TestRouteOverlapsHostnames(t *testing.T) {
	tests := []struct {
		name    string
		current []gateway.Hostname
		pending []gateway.Hostname
		want    bool
	}{
		{name: "same hostname", current: []gateway.Hostname{"portal.example.com"}, pending: []gateway.Hostname{"portal.example.com"}, want: true},
		{name: "removed hostname", current: []gateway.Hostname{"old.example.com"}, pending: []gateway.Hostname{"new.example.com"}},
		{name: "wildcard old route", current: []gateway.Hostname{"*.example.com"}, pending: []gateway.Hostname{"portal.example.com"}, want: true},
		{name: "wildcard new route", current: []gateway.Hostname{"portal.example.com"}, pending: []gateway.Hostname{"*.example.com"}, want: true},
		{name: "different domains", current: []gateway.Hostname{"*.example.com"}, pending: []gateway.Hostname{"portal.example.net"}},
		{name: "old route matching all hosts", pending: []gateway.Hostname{"portal.example.com"}, want: true},
		{name: "no desired replacement hosts", current: []gateway.Hostname{"portal.example.com"}},
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

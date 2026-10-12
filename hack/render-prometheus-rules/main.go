// Extract native rule groups from a PrometheusRule for promtool validation.
package main

import (
	"fmt"
	"os"

	"sigs.k8s.io/yaml"
)

func main() {
	if len(os.Args) != 2 {
		panic("usage: render-prometheus-rules <PrometheusRule.yaml>")
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	var rule struct {
		APIVersion string         `json:"apiVersion"`
		Kind       string         `json:"kind"`
		Metadata   map[string]any `json:"metadata"`
		Spec       map[string]any `json:"spec"`
	}
	if err = yaml.UnmarshalStrict(data, &rule); err != nil {
		panic(err)
	}
	if rule.Kind != "PrometheusRule" || rule.APIVersion != "monitoring.coreos.com/v1" {
		panic("expected monitoring.coreos.com/v1 PrometheusRule")
	}
	if rule.Spec["groups"] == nil {
		panic("missing rule groups")
	}
	data, err = yaml.Marshal(rule.Spec)
	if err != nil {
		panic(err)
	}
	fmt.Print(string(data))
}

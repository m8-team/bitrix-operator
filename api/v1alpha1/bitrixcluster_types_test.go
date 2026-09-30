/*
Copyright 2026 M8 Team.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSourceContractUnchanged(t *testing.T) {
	source, err := os.ReadFile("../../docs/reference/bitrixcluster_types.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile("bitrixcluster_types.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, actual) {
		t.Fatal("API source differs from the supplied contract")
	}
}

func TestJSONOptionalFieldsAndDuration(t *testing.T) {
	obj := BitrixCluster{}
	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"metadata", "status"} {
		if _, exists := fields[key]; exists {
			t.Errorf("zero %s must be omitted by omitzero", key)
		}
	}
	agents := AgentsSpec{Enabled: true, Interval: metav1.Duration{Duration: 30 * time.Second}}
	data, err = json.Marshal(agents)
	if err != nil {
		t.Fatal(err)
	}
	var decoded AgentsSpec
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Interval.Duration != 30*time.Second {
		t.Fatal("duration did not round-trip")
	}
	if err := json.Unmarshal([]byte(`{"enabled":true,"interval":"invalid"}`), &decoded); err == nil {
		t.Fatal("invalid duration must fail typed decoding")
	}
}

func TestDeepCopyIsolation(t *testing.T) {
	obj := &BitrixCluster{Spec: BitrixClusterSpec{
		Sites:   []BitrixSiteSpec{{ID: "s1", Hosts: []string{"portal.example.com"}, TLS: &SiteTLSSpec{SecretName: "tls"}}},
		Runtime: RuntimeSpec{Cron: CronRuntimeSpec{Enabled: true, Agents: &AgentsSpec{Enabled: true}}},
	}}
	copy := obj.DeepCopy()
	copy.Spec.Sites[0].Hosts[0] = "changed.example.com"
	copy.Spec.Sites[0].TLS.SecretName = "changed"
	copy.Spec.Runtime.Cron.Agents.Enabled = false
	if obj.Spec.Sites[0].Hosts[0] != "portal.example.com" || obj.Spec.Sites[0].TLS.SecretName != "tls" || !obj.Spec.Runtime.Cron.Agents.Enabled {
		t.Fatal("DeepCopy aliases mutable fields")
	}
}

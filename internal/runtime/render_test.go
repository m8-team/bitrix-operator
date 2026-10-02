package runtime

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	bitrixv1 "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"sigs.k8s.io/yaml"
)

func sample(t *testing.T) bitrixv1.BitrixClusterSpec {
	t.Helper()
	data, err := os.ReadFile("../../config/samples/bitrix_v1alpha1_bitrixcluster.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cluster bitrixv1.BitrixCluster
	if err = yaml.UnmarshalStrict(data, &cluster); err != nil {
		t.Fatal(err)
	}
	return cluster.Spec
}

func TestRenderDeterministicAndPrivate(t *testing.T) {
	spec := sample(t)
	spec.Database.Connections = append(spec.Database.Connections, bitrixv1.DatabaseConnectionSpec{
		Name: "analytics", Host: "db", Port: 3307, Database: "quotes'\"\\\n<?php die();", Username: "user",
		PasswordSecretRef: spec.Database.Connections[0].PasswordSecretRef})
	before := spec.DeepCopy()
	a, err := Render(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.Database.Connections[0], spec.Database.Connections[1] = spec.Database.Connections[1], spec.Database.Connections[0]
	b, err := Render(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("render depends on connection order")
	}
	if len(a.Secrets) != 4 {
		t.Fatal("selectors were not deduplicated")
	}
	if strings.Contains(a.Nginx["nginx.conf"], SecretRoot) {
		t.Fatal("nginx references Secrets")
	}
	var m manifest
	if err = json.Unmarshal([]byte(a.PHP["manifest.json"]), &m); err != nil {
		t.Fatal(err)
	}
	if m.Connections["analytics"].Database != before.Database.Connections[1].Database {
		t.Fatal("JSON changed literal database name")
	}
	for _, projection := range a.Secrets {
		if projection.Secret.Optional == nil || *projection.Secret.Optional || *projection.Secret.Items[0].Mode != 0440 {
			t.Fatal("unsafe secret projection")
		}
	}
	spec.Database.Connections[0], spec.Database.Connections[1] = spec.Database.Connections[1], spec.Database.Connections[0]
	if !reflect.DeepEqual(&spec, before) {
		t.Fatal("renderer mutated input")
	}
}

func TestUnsafeRuntimeConfiguration(t *testing.T) {
	cases := map[string]func(*bitrixv1.BitrixClusterSpec){
		"nginx injection":  func(s *bitrixv1.BitrixClusterSpec) { s.Sites[0].Hosts = []string{"example.com; include /etc/passwd;"} },
		"wildcard":         func(s *bitrixv1.BitrixClusterSpec) { s.Sites[0].Hosts = []string{"*.example.com"} },
		"duplicate host":   func(s *bitrixv1.BitrixClusterSpec) { s.Sites = append(s.Sites, s.Sites[0]) },
		"path traversal":   func(s *bitrixv1.BitrixClusterSpec) { s.Storage.Uploads.MountPath = Root + "/upload/../bitrix" },
		"source overwrite": func(s *bitrixv1.BitrixClusterSpec) { s.Storage.Uploads.MountPath = Root + "/bitrix/modules" },
		"temporary source": func(s *bitrixv1.BitrixClusterSpec) { s.Storage.Temporary.MountPath = Root + "/tmp" },
		"SQL injection": func(s *bitrixv1.BitrixClusterSpec) {
			s.Database.Connections[0].Options.Timezone = "'; DROP TABLE users;--"
		},
		"charset injection":  func(s *bitrixv1.BitrixClusterSpec) { s.Database.Connections[0].Options.Charset = "utf8;" },
		"optional secret":    func(s *bitrixv1.BitrixClusterSpec) { v := true; s.License.SecretRef.Optional = &v },
		"invalid key":        func(s *bitrixv1.BitrixClusterSpec) { s.Crypto.SecretRef.Key = "../key" },
		"unknown mode":       func(s *bitrixv1.BitrixClusterSpec) { s.Redis.Sessions.Mode = "anything" },
		"zero ttl":           func(s *bitrixv1.BitrixClusterSpec) { s.Redis.Sessions.TTL = 0 },
		"endpoint injection": func(s *bitrixv1.BitrixClusterSpec) { s.Database.Connections[0].Host = "db:3306:socket" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s := sample(t)
			change(&s)
			if _, err := Render(s); err == nil {
				t.Fatal("unsafe input accepted")
			}
		})
	}
}

func TestConnectionOptions(t *testing.T) {
	for _, engine := range []bitrixv1.DatabaseEngine{bitrixv1.DatabaseEngineMySQL, bitrixv1.DatabaseEnginePostgreSQL} {
		t.Run(string(engine), func(t *testing.T) {
			s := sample(t)
			s.Database.Engine = engine
			s.Database.Connections[0].Options = bitrixv1.DatabaseOptionsSpec{Charset: "utf8", Timezone: "Europe/Moscow"}
			b, err := Render(s)
			if err != nil {
				t.Fatal(err)
			}
			var m manifest
			if err = json.Unmarshal([]byte(b.PHP["manifest.json"]), &m); err != nil {
				t.Fatal(err)
			}
			command := m.Connections["default"].InitCommand
			if !strings.Contains(command, "utf8") || !strings.Contains(command, "Europe/Moscow") {
				t.Fatal("connection options lost")
			}
		})
	}
}

func TestReleaseReference(t *testing.T) {
	image := bitrixv1.ReleaseImageSpec{Repository: "registry.example.com/team/app", Tag: "release", Digest: "sha256:" + strings.Repeat("a", 64)}
	ref, err := ReleaseReference(image)
	if err != nil || ref != image.Repository+"@"+image.Digest {
		t.Fatal("digest did not take precedence")
	}
	image.Digest = "sha256:abcd"
	if _, err = ReleaseReference(image); err == nil {
		t.Fatal("short digest accepted")
	}
	image.Digest = ""
	ref, err = ReleaseReference(image)
	if err != nil || ref != image.Repository+":release" {
		t.Fatal("tag reference failed")
	}
}

func TestSiteResults(t *testing.T) {
	id := ValidationIdentity{ClusterUID: "uid", Generation: 4, Release: "sha256:" + strings.Repeat("a", 64), PHP: "sha256:" + strings.Repeat("b", 64), Attempt: "job-uid"}
	valid := SiteResult{Version: ContractVersion, Identity: id, Sites: []string{"s1"}, Reason: "Validated"}
	encode := func(v SiteResult) []byte {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if _, err := ParseSiteResult(encode(valid), id, []string{"s1"}); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*SiteResult){
		func(r *SiteResult) { r.Identity.Generation++ }, func(r *SiteResult) { r.Identity.PHP = "sha256:" + strings.Repeat("c", 64) }, func(r *SiteResult) { r.Identity.Attempt = "old" },
		func(r *SiteResult) { r.Sites = []string{"s2"} }, func(r *SiteResult) { r.Sites = []string{"s1", "s1"} },
		func(r *SiteResult) { r.Sites = nil }, func(r *SiteResult) { r.Reason = "password leaked" },
	} {
		r := valid
		change(&r)
		if _, err := ParseSiteResult(encode(r), id, []string{"s1"}); err == nil {
			t.Fatal("invalid result accepted")
		}
	}
	for _, data := range [][]byte{nil, []byte(strings.Repeat("x", 4097)), append(encode(valid), []byte(" {}")...), []byte(`{"unknown":"field"}`)} {
		if _, err := ParseSiteResult(data, id, []string{"s1"}); err == nil {
			t.Fatal("invalid JSON accepted")
		}
	}
}

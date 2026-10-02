package runtime

import (
	_ "embed"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

const pushImage = "quay.io/bitrix24/push:3.3-v1-alpine@sha256:7ea0b71d66ab6cc9b0b58b6ba853d4398880fdfb1aa19b434a3bf462043bfecc"

//go:embed testdata/check-push.cjs
var pushCheck string

func pushSpec(t *testing.T) bx.BitrixClusterSpec {
	t.Helper()
	s := sample(t)
	s.Runtime.Push = &bx.PushRuntimeSpec{Enabled: true, Image: pushImage, SecretRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "push-key"}, Key: "key"}, Publisher: bx.ReplicaSpec{Replicas: 1}, Subscriber: bx.ReplicaSpec{Replicas: 2}}
	return s
}

func TestPushSecretIsolation(t *testing.T) {
	s := pushSpec(t)
	b, err := Render(s, PushEndpoints{Publisher: "publisher", Subscriber: "subscriber"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range b.PushSecrets {
		if p.Secret.Name != s.Runtime.Push.SecretRef.Name && p.Secret.Name != s.Redis.PasswordSecretRef.Name {
			t.Fatal("application credentials exposed to Push")
		}
	}
	if len(b.PushSecrets) != 2 || len(b.Push) != 6 {
		t.Fatal("incomplete Push bundle")
	}
	if _, err = Render(s, PushEndpoints{Publisher: "bad; injected", Subscriber: "subscriber"}); err == nil {
		t.Fatal("unsafe upstream accepted")
	}
	if !strings.Contains(b.PHP["manifest.json"], `"publisher": "publisher"`) {
		t.Fatal("missing PHP publisher")
	}
	s.Runtime.Push.Enabled = false
	disabled, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled.Push) != 0 || len(disabled.PushSecrets) != 0 || strings.Contains(disabled.Nginx["nginx.conf"], "proxy_pass http://bitrix_push") {
		t.Fatal("disabled Push remained configured")
	}
}

func testPushContainers(t *testing.T) {
	prefix := "bitrix-push-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	network := prefix + "-network"
	docker(t, "network", "create", network)
	t.Cleanup(func() { _ = exec.Command("docker", "network", "rm", network).Run() })
	start := func(name string, args ...string) {
		t.Helper()
		docker(t, args...)
		t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	}
	// Synthetic test password; production uses a projected Secret file.
	password := "push-test-\"quote\\\nпароль"
	redis := prefix + "-redis"
	start(redis, "run", "--rm", "-d", "--name", redis, "--network", network, "--network-alias", "redis", "redis:8.2.10-alpine", "redis-server", "--requirepass", password, "--save", "", "--appendonly", "no")
	s := pushSpec(t)
	s.Redis.Host = "redis"
	b, err := Render(s, PushEndpoints{Publisher: "publisher", Subscriber: "subscriber"})
	if err != nil {
		t.Fatal(err)
	}
	b.Push["check-push.cjs"] = pushCheck
	config := writeFiles(t, b.Push)
	secrets := map[string]string{}
	for _, p := range b.PushSecrets {
		for _, item := range p.Secret.Items {
			value := password
			if p.Secret.Name == s.Runtime.Push.SecretRef.Name {
				value = "\ufeffquote\"\\\nключ" + strings.Repeat("k", 64)
			}
			secrets[item.Path] = value
		}
	}
	secretDir := writeFiles(t, secrets)
	var publisher string
	for _, alias := range []string{"publisher", "subscriber", "subscriber2"} {
		role := "subscriber"
		if alias == "publisher" {
			role = alias
		}
		name := prefix + "-" + alias
		args := containerArgs()
		for i := range args {
			if args[i] == "--network=none" {
				args[i] = "--network=" + network
			}
		}
		args = append(args, "-d", "--name", name, "--network-alias", alias, "--network-alias", alias+".bitrix.test", "-v", config+":/run/push-config:ro", "-v", secretDir+":"+SecretRoot+":ro", "--entrypoint=/bin/sh", pushImage, "/run/push-config/start.sh", role)
		start(name, args...)
		deadline := time.Now().Add(40 * time.Second)
		for exec.Command("docker", "exec", name, "node", "/run/push-config/health.cjs", "ready").Run() != nil {

			if time.Now().After(deadline) {
				t.Fatal("Push did not become ready: " + docker(t, "logs", name))
			}
			time.Sleep(250 * time.Millisecond)
		}
		if role == "publisher" {
			publisher = name
		}
	}
	if !strings.Contains(docker(t, "exec", publisher, "node", "/run/push-config/check-push.cjs"), "Push protocol OK") {
		t.Fatal("missing protocol result")
	}
	// Exercise the same native nginx startup and routes as Kubernetes, using Docker DNS.
	nginxConfig := writeFiles(t, b.Nginx)
	nginxName := prefix + "-nginx"
	args := containerArgs()
	for i := range args {
		if args[i] == "--network=none" {
			args[i] = "--network=" + network
		}
	}
	args = append(args, "-d", "--name", nginxName, "--network-alias", "nginx", "--dns-search", "bitrix.test", "-v", nginxConfig+":/run/nginx:ro", "--entrypoint=/bin/sh", nginxImage, "/run/nginx/start.sh")
	start(nginxName, args...)
	deadline := time.Now().Add(20 * time.Second)
	for exec.Command("docker", "exec", nginxName, "wget", "-qO-", "--header=Host: portal.example.com", "http://127.0.0.1:8080/_operator/health").Run() != nil {
		if time.Now().After(deadline) {
			t.Fatal("nginx did not become ready: " + docker(t, "logs", nginxName))
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Log("Testing Push through native nginx proxy")
	if !strings.Contains(docker(t, "exec", publisher, "node", "/run/push-config/check-push.cjs", "http://publisher:9010", "http://nginx:8080,http://nginx:8080", "http://nginx:8080", "/bitrix/sub/"), "Push protocol OK") {
		t.Fatal("missing proxied protocol result")
	}
	// Missing Secret values fail closed with a generic diagnostic before starting the server.
	for path := range secrets {
		if err = os.Remove(secretDir + "/" + path); err != nil {
			t.Fatal(err)
		}
		break
	}
	output, err := exec.Command("docker", "exec", publisher, "node", "/run/push-config/configure.cjs", "publisher").CombinedOutput()
	if err == nil || !strings.Contains(string(output), "Push configuration unavailable") {
		t.Fatal("missing Secret did not fail closed")
	}
}

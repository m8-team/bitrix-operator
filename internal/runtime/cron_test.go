package runtime

import (
	_ "embed"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//go:embed testdata/check-cron.php
var cronCheck string

func testCronContainer(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		spec := sample(t)
		spec.Runtime.Cron.Enabled = true
		spec.Runtime.Cron.Agents = &bx.AgentsSpec{Enabled: enabled, Interval: metav1.Duration{Duration: 1200 * time.Millisecond}}
		b, err := Render(spec)
		if err != nil {
			t.Fatal(err)
		}
		b.PHP["check-cron.php"] = cronCheck
		config := writeFiles(t, b.PHP)
		args := append(containerArgs(), "-v", config+":"+ConfigRoot+":ro", "--tmpfs=/opt/www/upload:rw,nosuid,size=16m,uid=979,gid=979", "--entrypoint=php", "bitrix-runtime-fixture", ConfigRoot+"/check-cron.php")
		if !strings.Contains(docker(t, args...), "OK") {
			t.Fatal("missing cron test result")
		}
	}
	t.Run("image-stop-signal", testCronImageStop)
}

// Docker uses the image's StopSignal, which can differ from SIGTERM.
// Exercise the actual PID 1, not only a shell launched as a test subprocess.
func testCronImageStop(t *testing.T) {
	t.Logf("Cron image stop signal: %s", strings.TrimSpace(docker(t, "image", "inspect", "--format={{.Config.StopSignal}}", "bitrix-runtime-fixture")))
	spec := sample(t)
	spec.Runtime.Cron.Enabled = true
	spec.Runtime.Cron.Agents = &bx.AgentsSpec{Enabled: true, Interval: metav1.Duration{Duration: time.Second}}
	b, err := Render(spec)
	if err != nil {
		t.Fatal(err)
	}
	config := writeFiles(t, b.PHP)
	uploads := t.TempDir()
	if err = os.Chmod(uploads, 0777); err != nil {
		t.Fatal(err)
	}
	name := "bitrix-cron-stop-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	args := append(containerArgs(), "-d", "--name", name, "-v", config+":"+ConfigRoot+":ro", "-v", uploads+":/opt/www/upload",
		"--entrypoint=/bin/sh", "bitrix-runtime-fixture", ConfigRoot+"/scheduler.sh")
	docker(t, args...)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(uploads, "starts")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not start before stop-signal test")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The active fixture agent needs 1.6 seconds. A forced kill after 5 seconds
	// leaves missing completion records and/or an active marker.
	docker(t, "stop", "--time=5", name)
	starts, err := os.ReadFile(filepath.Join(uploads, "starts"))
	if err != nil {
		t.Fatal(err)
	}
	ends, err := os.ReadFile(filepath.Join(uploads, "ends"))
	if err != nil {
		t.Fatal("image stop signal interrupted the active agent", err)
	}
	if strings.Count(string(starts), "\n") != 1 || strings.Count(string(ends), "\n") != 1 {
		t.Fatal("scheduler continued after image stop signal")
	}
	if _, err = os.Stat(filepath.Join(uploads, "active")); !os.IsNotExist(err) {
		t.Fatal("agent was not drained")
	}
}

func TestCronIntervalValidation(t *testing.T) {
	for _, interval := range []time.Duration{-time.Second, 0, time.Millisecond, 25 * time.Hour} {
		spec := sample(t)
		spec.Runtime.Cron.Enabled = true
		spec.Runtime.Cron.Agents = &bx.AgentsSpec{Enabled: true, Interval: metav1.Duration{Duration: interval}}
		if _, err := Render(spec); err == nil {
			t.Fatalf("accepted invalid interval %s", interval)
		}
	}
}

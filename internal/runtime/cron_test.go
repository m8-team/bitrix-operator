package runtime

import (
	_ "embed"
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

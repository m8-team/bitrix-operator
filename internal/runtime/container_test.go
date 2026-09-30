package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const nginxImage = "quay.io/bitrix24/nginx:1.30.5-v1-alpine@sha256:77d042de08856d8644aad10c0bc744790e228b7837b76b2b8644159b073fcfb5"

// TestRuntimeContainers is mandatory in the runtime CI job, opt-in for local Go/envtest runs.
func TestRuntimeContainers(t *testing.T) {
	if os.Getenv("BITRIX_RUNTIME_CONTAINER_TESTS") != "1" {
		t.Skip("set BITRIX_RUNTIME_CONTAINER_TESTS=1 and build bitrix-runtime-fixture to run Docker checks")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"default", "separated"} {
		t.Run(mode, func(t *testing.T) { testPHPContainer(t, mode) })
	}
	t.Run("nginx", testNginxContainer)
}

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	// Docker's arbitrary UID must be able to traverse the bind mount root.
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
func docker(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("container command failed: %v\n%s", err, output)
	}
	return string(output)
}
func containerArgs() []string {
	return []string{"run", "--rm", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user=979:979", "--tmpfs=/tmp:rw,noexec,nosuid,size=32m"}
}
func testPHPContainer(t *testing.T, mode string) {
	spec := sample(t)
	spec.Redis.Sessions.Mode = mode
	b, err := Render(spec)
	if err != nil {
		t.Fatal(err)
	}
	b.PHP["check.php"] = phpCheck
	config := writeFiles(t, b.PHP)
	secretFiles := map[string]string{}
	// Adversarial synthetic value, never a real credential.
	for _, projection := range b.Secrets {
		secretFiles[projection.Secret.Items[0].Path] = "quote'\"\\\n$variable ?> <?php throw new Exception();\n"
	}
	secrets := writeFiles(t, secretFiles)
	args := append(containerArgs(), "-v", config+":"+ConfigRoot+":ro", "-v", secrets+":"+SecretRoot+":ro",
		"--entrypoint=sh", "bitrix-runtime-fixture", "-ec", `
for file in /run/bitrix-config/*.php /opt/www/bitrix/.*.php; do php -l "$file"; done
php-fpm -c /run/bitrix-config/php.ini -y /run/bitrix-config/php-fpm.conf -t
php -c /run/bitrix-config/php.ini /run/bitrix-config/check.php
`)
	output := docker(t, args...)
	if !strings.Contains(output, "runtime semantics OK") {
		t.Fatal("missing semantic test result")
	}
	// Missing Secret must fail with a sanitized error; never print exception args or password content.
	for name := range secretFiles {
		if err = os.Remove(filepath.Join(secrets, name)); err != nil {
			t.Fatal(err)
		}
		break
	}
	args[len(args)-1] = `php -d zend.exception_ignore_args=1 -r 'try { require "/opt/www/bitrix/.settings.php"; require "/run/bitrix-config/license_key.php"; exit(2); } catch (Throwable $e) { if ($e->getMessage() !== "Required runtime Secret is unavailable") { exit(3); } echo "sanitized failure"; }'`
	if !strings.Contains(docker(t, args...), "sanitized failure") {
		t.Fatal("missing sanitized failure")
	}
}
func testNginxContainer(t *testing.T) {
	b, err := Render(sample(t))
	if err != nil {
		t.Fatal(err)
	}
	dir := writeFiles(t, b.Nginx)
	args := append(containerArgs(), "-v", dir+":/run/nginx:ro", "--entrypoint=nginx", nginxImage, "-t", "-c", "/run/nginx/nginx.conf")
	docker(t, args...)
}

const phpCheck = `<?php
function check(bool $ok): void { if (!$ok) { throw new RuntimeException('Runtime semantic check failed'); } }
foreach (['mysqli', 'pgsql', 'redis', 'mbstring', 'xml', 'gd', 'zip', 'intl'] as $extension) {
    check(extension_loaded($extension));
}
$m = json_decode(file_get_contents('/run/bitrix-config/manifest.json'), true, 64, JSON_THROW_ON_ERROR);
$s = require '/opt/www/bitrix/.settings.php';
$secret = file_get_contents($m['crypto']);
check($s['custom']['value']['marker'] === 'base-preserved');
check($s['custom']['value']['directory'] === '/opt/www/bitrix');
check($s['extra']['value']['marker'] === 'extra-preserved');
check($s['connections']['value']['analytics']['className'] === 'CustomConnection');
check($s['connections']['value']['default']['password'] === $secret);
check(!isset($s['connections']['value']['default']['passwordFile']));
check($s['cache']['value']['redis']['password'] === $secret);
check($s['cache']['value']['use_lock'] === true);
check($s['session']['value']['handlers']['general']['auth'] === $secret);
check($s['session']['value']['lifetime'] === 1440);
check($s['session']['value']['regenerateIdAfterLogin'] === true);
check(($s['session']['value']['handlers']['kernel'] ?? null) === ($m['sessionMode'] === 'separated' ? 'encrypted_cookies' : null));
check($s['crypto']['value']['crypto_key'] === $secret);
require '/run/bitrix-config/license_key.php';
check($LICENSE_KEY === $secret);
check((require '/opt/www/bitrix/.settings_extra.php') === []);
echo "runtime semantics OK\n";
`

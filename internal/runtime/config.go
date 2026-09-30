package runtime

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"k8s.io/apimachinery/pkg/util/validation"
)

// Only native component files are embedded; documentation and golden files are excluded.
//
//go:embed templates/nginx/* templates/php/* templates/bitrix/*
var configFiles embed.FS

type configData struct {
	Hosts         []string
	UploadsPath   string
	TemporaryPath string
}

// renderConfigs has an explicit output list so build-only PHP files cannot become runtime mounts.
func renderConfigs(data configData) (map[string]string, map[string]string, error) {
	nginx := map[string]string{}
	php := map[string]string{}
	files := []struct {
		source string
		name   string
		target map[string]string
	}{
		{"nginx/nginx.conf.tmpl", "nginx.conf", nginx},
		{"php/php-fpm.conf", "php-fpm.conf", php},
		{"php/php.ini.tmpl", "php.ini", php},
		{"bitrix/init-release.sh", "init-release.sh", php},
		{"bitrix/fpm-live.php", "fpm-live.php", php},
		{"bitrix/ready.php", "ready.php", php},
		{"bitrix/settings.php", "settings.php", php},
		{"bitrix/settings-wrapper.php", "settings-wrapper.php", php},
		{"bitrix/license_key.php", "license_key.php", php},
	}
	for _, file := range files {
		content, err := renderFile(configFiles, "templates/"+file.source, data)
		if err != nil {
			return nil, nil, err
		}
		file.target[file.name] = content
	}
	return nginx, php, nil
}

// renderFile interprets only .tmpl files. Native PHP and static configuration are copied byte-for-byte.
// Templates are trusted repository files, never CR-provided template text.
func renderFile(source fs.FS, name string, data any) (string, error) {
	content, err := fs.ReadFile(source, name)
	if err != nil {
		return "", fmt.Errorf("read config file %s: %w", name, err)
	}
	if !strings.HasSuffix(name, ".tmpl") {
		return string(content), nil
	}
	parsed, err := template.New(name).Option("missingkey=error").Funcs(template.FuncMap{
		"nginxHosts":          nginxHosts,
		"nginxUploadsPattern": nginxUploadsPattern,
		"iniPath":             iniPath,
	}).Parse(string(content))
	if err != nil {
		return "", fmt.Errorf("parse config template %s: %w", name, err)
	}
	var output bytes.Buffer
	if err = parsed.Execute(&output, data); err != nil {
		// Do not return partially rendered files or include data in error diagnostics.
		return "", fmt.Errorf("execute config template %s: %w", name, err)
	}
	return output.String(), nil
}

func nginxHosts(hosts []string) (string, error) {
	if len(hosts) == 0 {
		return "", fmt.Errorf("site hosts are required")
	}
	for _, host := range hosts {
		if len(validation.IsDNS1123Subdomain(host)) != 0 || net.ParseIP(host) != nil {
			return "", fmt.Errorf("site hosts must be lowercase DNS names")
		}
	}
	ordered := slices.Clone(hosts)
	slices.Sort(ordered)
	return strings.Join(ordered, " "), nil
}

func nginxUploadsPattern(uploads string) (string, error) {
	if err := validatePaths(uploads, "/opt/.bx_temp"); err != nil {
		return "", err
	}
	return regexp.QuoteMeta(strings.TrimPrefix(uploads, Root)), nil
}

func iniPath(temp string) (string, error) {
	if err := validatePaths(Root+"/upload", temp); err != nil {
		return "", err
	}
	// Paths have already passed a strict ASCII allowlist; quoting preserves INI syntax.
	return strconv.Quote(temp), nil
}

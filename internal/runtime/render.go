// Package runtime renders the versioned Bitrix runtime contract without reading Secret data.
package runtime

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	bitrixv1 "github.com/m8-team/bitrix-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	// ContractVersion changes when a release image needs a different runtime interface.
	ContractVersion = "v1"
	// Root is shared application code, copied from the release into a per-Pod EmptyDir.
	Root = "/opt/www"
	// ConfigRoot and SecretRoot must be mounted in PHP containers only.
	ConfigRoot = "/run/bitrix-config"
	SecretRoot = "/run/bitrix-secrets"
)

// Bundle contains no secret values. Keep PHP configuration separate from nginx mounts.
type Bundle struct {
	Nginx   map[string]string
	PHP     map[string]string
	Secrets []corev1.VolumeProjection
	Hash    string
}

type manifest struct {
	Version     string                    `json:"version"`
	Engine      bitrixv1.DatabaseEngine   `json:"engine"`
	Connections map[string]connectionData `json:"connections"`
	Redis       redisData                 `json:"redis"`
	SessionMode string                    `json:"sessionMode"`
	SessionTTL  int64                     `json:"sessionTTL"`
	CacheSID    string                    `json:"cacheSID"`
	License     string                    `json:"license"`
	Crypto      string                    `json:"crypto"`
}

// Field order preserves the manifest JSON emitted before the template migration.
type connectionData struct {
	ClassName    string `json:"className"`
	Database     string `json:"database"`
	Host         string `json:"host"`
	InitCommand  string `json:"initCommand"`
	Login        string `json:"login"`
	Options      int    `json:"options"`
	PasswordFile string `json:"passwordFile"`
}

type redisData struct {
	Host         string `json:"host"`
	PasswordFile string `json:"passwordFile"`
	Port         int32  `json:"port"`
}

// Render validates the runtime subset of an already API-validated spec and produces deterministic files.
func Render(spec bitrixv1.BitrixClusterSpec) (*Bundle, error) {
	hosts, err := validate(spec)
	if err != nil {
		return nil, err
	}
	refs := newSecretSet()
	m := manifest{Version: ContractVersion, Engine: spec.Database.Engine,
		Connections: map[string]connectionData{}, SessionMode: spec.Redis.Sessions.Mode,
		SessionTTL: spec.Redis.Sessions.TTL, CacheSID: spec.Redis.Cache.SID}
	if m.License, err = refs.add(spec.License.SecretRef); err != nil {
		return nil, err
	}
	if m.Crypto, err = refs.add(spec.Crypto.SecretRef); err != nil {
		return nil, err
	}
	password, err := refs.add(spec.Redis.PasswordSecretRef)
	if err != nil {
		return nil, err
	}
	m.Redis = redisData{Host: spec.Redis.Host, Port: spec.Redis.Port, PasswordFile: password}
	for _, c := range spec.Database.Connections {
		password, err = refs.add(c.PasswordSecretRef)
		if err != nil {
			return nil, err
		}
		class := "\\Bitrix\\Main\\DB\\MysqliConnection"
		if spec.Database.Engine == bitrixv1.DatabaseEnginePostgreSQL {
			class = "\\Bitrix\\Main\\DB\\PgsqlConnection"
		}
		m.Connections[c.Name] = connectionData{ClassName: class,
			Host: c.Host + ":" + strconv.Itoa(int(c.Port)), Database: c.Database, Login: c.Username,
			PasswordFile: password, Options: 2, InitCommand: initCommand(spec.Database.Engine, c.Options)}
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode runtime manifest: %w", err)
	}
	nginx, php, err := renderConfigs(configData{Hosts: hosts,
		UploadsPath: spec.Storage.Uploads.MountPath, TemporaryPath: spec.Storage.Temporary.MountPath})
	if err != nil {
		return nil, err
	}
	php["manifest.json"] = string(data)
	b := &Bundle{Nginx: nginx, PHP: php, Secrets: refs.projections()}
	// JSON sorts map keys; projections are explicitly sorted. No secret bytes enter this hash.
	encoded, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	b.Hash = fmt.Sprintf("%x", sha256.Sum256(encoded))
	return b, nil
}

var safeToken = regexp.MustCompile(`^[A-Za-z0-9_./:+-]+$`)
var safeCharset = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

func validate(s bitrixv1.BitrixClusterSpec) ([]string, error) {
	if s.Database.Engine != bitrixv1.DatabaseEngineMySQL && s.Database.Engine != bitrixv1.DatabaseEnginePostgreSQL {
		return nil, fmt.Errorf("unsupported database engine")
	}
	if s.Redis.Sessions.Mode != "default" && s.Redis.Sessions.Mode != "separated" {
		return nil, fmt.Errorf("unsupported session mode")
	}
	if s.Redis.Sessions.TTL <= 0 {
		return nil, fmt.Errorf("runtime requires a positive session TTL")
	}
	if !endpoint(s.Redis.Host) || s.Redis.Port < 1 || s.Redis.Port > 65535 {
		return nil, fmt.Errorf("invalid Redis endpoint")
	}
	if s.Redis.Cache.SID == "" {
		return nil, fmt.Errorf("cache SID is required")
	}
	if err := validatePaths(s.Storage.Uploads.MountPath, s.Storage.Temporary.MountPath); err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, c := range s.Database.Connections {
		if names[c.Name] || c.Name == "" {
			return nil, fmt.Errorf("duplicate or empty connection name")
		}
		names[c.Name] = true
		if !endpoint(c.Host) || c.Port < 1 || c.Port > 65535 {
			return nil, fmt.Errorf("invalid database endpoint")
		}
		if c.Options.Charset != "" && !safeCharset.MatchString(c.Options.Charset) {
			return nil, fmt.Errorf("invalid database charset")
		}
		if c.Options.Timezone != "" && !safeToken.MatchString(c.Options.Timezone) {
			return nil, fmt.Errorf("invalid database timezone")
		}
	}
	if !names["default"] {
		return nil, fmt.Errorf("default database connection is required")
	}
	hosts := []string{}
	seen := map[string]bool{}
	for _, site := range s.Sites {
		for _, host := range site.Hosts {
			if len(validation.IsDNS1123Subdomain(host)) != 0 || net.ParseIP(host) != nil {
				return nil, fmt.Errorf("site hosts must be lowercase DNS names")
			}
			if seen[host] {
				return nil, fmt.Errorf("host belongs to multiple sites")
			}
			seen[host] = true
			hosts = append(hosts, host)
		}
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("at least one site host is required")
	}
	slices.Sort(hosts)
	return hosts, nil
}

func endpoint(host string) bool {
	// Bitrix's host:port parser does not provide a portable IPv6 contract across DB versions.
	return !strings.Contains(host, ":") && (net.ParseIP(host) != nil || len(validation.IsDNS1123Subdomain(host)) == 0)
}

func validatePaths(uploads, temp string) error {
	for _, p := range []string{uploads, temp} {
		if !strings.HasPrefix(p, "/") || path.Clean(p) != p || !safeToken.MatchString(p) {
			return fmt.Errorf("unsafe runtime mount path")
		}
	}
	if !strings.HasPrefix(uploads, Root+"/") || uploads == Root+"/bitrix" || strings.HasPrefix(uploads, Root+"/bitrix/") || uploads == Root+"/local" || strings.HasPrefix(uploads, Root+"/local/") {
		return fmt.Errorf("uploads must be below document root and outside application code directories")
	}
	if temp == "/" || temp == "/opt" || temp == Root || strings.HasPrefix(temp, Root+"/") || !strings.HasPrefix(temp, "/opt/") {
		return fmt.Errorf("temporary storage must be below /opt and outside document root")
	}
	return nil
}

func initCommand(engine bitrixv1.DatabaseEngine, o bitrixv1.DatabaseOptionsSpec) string {
	parts := []string{}
	if engine == bitrixv1.DatabaseEngineMySQL {
		if o.Charset != "" {
			for _, key := range []string{"character_set_client", "character_set_connection", "character_set_results"} {
				parts = append(parts, key+" = '"+o.Charset+"'")
			}
		}
		if o.Timezone != "" {
			parts = append(parts, "time_zone = '"+o.Timezone+"'")
		}
		if len(parts) > 0 {
			return "SET " + strings.Join(parts, ", ")
		}
	} else {
		if o.Charset != "" {
			parts = append(parts, "SET client_encoding TO '"+o.Charset+"'")
		}
		if o.Timezone != "" {
			parts = append(parts, "SET TIME ZONE '"+o.Timezone+"'")
		}
	}
	return strings.Join(parts, "; ")
}

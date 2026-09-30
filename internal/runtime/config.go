package runtime

import (
	"fmt"
	"regexp"
	"strings"
)

func nginx(hosts []string, uploads string) string {
	return fmt.Sprintf(`worker_processes auto;
pid /tmp/nginx.pid;
error_log /dev/stderr warn;
events { worker_connections 1024; }
http {
 include /etc/nginx/mime.types;
 default_type application/octet-stream;
 access_log /dev/stdout;
 server_tokens off;
 client_body_temp_path /tmp/client-body;
 fastcgi_temp_path /tmp/fastcgi;
 proxy_temp_path /tmp/proxy;
 uwsgi_temp_path /tmp/uwsgi;
 scgi_temp_path /tmp/scgi;
 client_max_body_size 1024m;
 sendfile on;
 server { listen 8080 default_server; server_name _; return 444; }
 server {
  listen 8080;
  server_name %s;
  root /opt/www;
  index index.php;
  location = /_operator/health { access_log off; return 200 'ok'; }
  location ~ /\. { deny all; }
  location ~* ^/(?:bitrix|local)/(?:php_interface|modules)/ { deny all; }
  location ~* ^/bitrix/(?:license_key\.php|backup(?:/|$)|updates(?:/|$)) { deny all; }
  location ~* ^%s/.*\.(?:php[0-9]*|phtml|phar)(?:/|$) { deny all; }
  location ~* \.(?:bak|sql|log|ini|conf|sh|dist)$ { deny all; }
  location / { try_files $uri $uri/ /bitrix/urlrewrite.php?$query_string; }
  location ~ \.php$ {
   try_files $uri =404;
   fastcgi_pass 127.0.0.1:9000;
   fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
   fastcgi_param QUERY_STRING $query_string;
   fastcgi_param REQUEST_METHOD $request_method;
   fastcgi_param CONTENT_TYPE $content_type;
   fastcgi_param CONTENT_LENGTH $content_length;
   fastcgi_param SCRIPT_NAME $fastcgi_script_name;
   fastcgi_param REQUEST_URI $request_uri;
   fastcgi_param DOCUMENT_URI $document_uri;
   fastcgi_param DOCUMENT_ROOT $document_root;
   fastcgi_param SERVER_PROTOCOL $server_protocol;
   fastcgi_param REQUEST_SCHEME $scheme;
   fastcgi_param HTTPS $https if_not_empty;
   fastcgi_param GATEWAY_INTERFACE CGI/1.1;
   fastcgi_param SERVER_SOFTWARE nginx;
   fastcgi_param REMOTE_ADDR $remote_addr;
   fastcgi_param REMOTE_PORT $remote_port;
   fastcgi_param SERVER_ADDR $server_addr;
   fastcgi_param SERVER_PORT $server_port;
   fastcgi_param SERVER_NAME $host;
   fastcgi_param HTTP_PROXY "";
   fastcgi_param REDIRECT_STATUS 200;
   fastcgi_read_timeout 300s;
  }
  location ~* \.(?:php[0-9]*|phtml|phar)(?:[^a-z0-9]|$) { deny all; }
 }
}
`, strings.Join(hosts, " "), regexp.QuoteMeta(strings.TrimPrefix(uploads, Root)))
}

const fpm = `[global]
error_log = /proc/self/fd/2
daemonize = no
[www]
listen = 127.0.0.1:9000
listen.allowed_clients = 127.0.0.1
pm = ondemand
pm.max_children = 5
pm.process_idle_timeout = 10s
pm.max_requests = 500
clear_env = yes
catch_workers_output = yes
decorate_workers_output = no
security.limit_extensions = .php
ping.path = /_operator/fpm-ping
`

func phpINI(temp string) string {
	return fmt.Sprintf(`; Runtime contract v1. Load after the image's extension INI files.
short_open_tag = On
memory_limit = 512M
upload_max_filesize = 1024M
post_max_size = 1024M
max_execution_time = 300
max_input_vars = 10000
expose_php = Off
display_errors = Off
log_errors = On
zend.exception_ignore_args = On
cgi.fix_pathinfo = 0
session.name = BITRIX_PHPSESSID
session.cookie_httponly = 1
session.cookie_samesite = Lax
session.use_strict_mode = 1
opcache.validate_timestamps = 0
upload_tmp_dir = "%s"
sys_temp_dir = "%s"
`, temp, temp)
}

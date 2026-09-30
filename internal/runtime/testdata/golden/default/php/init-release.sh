#!/bin/sh
set -eu
# No Secret volume is mounted in this init container.
test -f /opt/www/bitrix/.settings.php
test -f /opt/www/bitrix/.settings.release.php
cp -R /opt/www/. /work/code/
mkdir -p /work/temporary/cache /work/temporary/managed_cache /work/temporary/stack_cache /work/temporary/html_pages

mkdir -p "/work/code/${1#/opt/www/}"
for directory in cache managed_cache stack_cache html_pages; do
    mkdir -p "/work/code/bitrix/$directory"
done

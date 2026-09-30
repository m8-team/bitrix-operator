#!/bin/sh
# Run only at image build time on a disposable copy of the application.
set -eu
root=${1:?usage: prepare-release.sh DOCUMENT_ROOT ASSETS_DIRECTORY}
assets=${2:?assets directory required}
test -f "$root/bitrix/.settings.php"
test ! -e "$root/bitrix/.settings.release.php"
test ! -e "$root/bitrix/.settings_extra.release.php"
# Keep original PHP in its original directory so __DIR__ continues to work.
mv "$root/bitrix/.settings.php" "$root/bitrix/.settings.release.php"
if [ -f "$root/bitrix/.settings_extra.php" ]; then
    mv "$root/bitrix/.settings_extra.php" "$root/bitrix/.settings_extra.release.php"
fi
cp "$assets/settings-wrapper.php" "$root/bitrix/.settings.php"
printf '%s\n' '<?php return [];' > "$root/bitrix/.settings_extra.php"
# No real license value may be baked into an image.
printf '%s\n' '<?php // License is mounted at runtime.' > "$root/bitrix/license_key.php"

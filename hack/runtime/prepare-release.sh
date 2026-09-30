#!/bin/sh
# Run only at image build time on a disposable copy of the application.
set -eu
root=${1:?usage: prepare-release.sh DOCUMENT_ROOT BITRIX_TEMPLATES_DIRECTORY}
templates=${2:?Bitrix templates directory required}
test -f "$templates/settings-wrapper.php"
test -f "$templates/settings-extra.php"
test -f "$templates/license-placeholder.php"
test -f "$root/bitrix/.settings.php"
test ! -e "$root/bitrix/.settings.release.php"
test ! -e "$root/bitrix/.settings_extra.release.php"
# Keep original PHP in its original directory so __DIR__ continues to work.
mv "$root/bitrix/.settings.php" "$root/bitrix/.settings.release.php"
if [ -f "$root/bitrix/.settings_extra.php" ]; then
    mv "$root/bitrix/.settings_extra.php" "$root/bitrix/.settings_extra.release.php"
fi
cp "$templates/settings-wrapper.php" "$root/bitrix/.settings.php"
cp "$templates/settings-extra.php" "$root/bitrix/.settings_extra.php"
# No real license value may be baked into an image.
cp "$templates/license-placeholder.php" "$root/bitrix/license_key.php"

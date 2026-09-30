package runtime

import (
	"fmt"
	"regexp"
	"strings"

	bitrixv1 "github.com/m8-team/bitrix-operator/api/v1alpha1"
)

var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var tagPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)

// ReleaseReference prefers the digest; the tag remains human-readable API metadata.
func ReleaseReference(image bitrixv1.ReleaseImageSpec) (string, error) {
	if image.Repository == "" || strings.ContainsAny(image.Repository, "@ \t\n\r") || !tagPattern.MatchString(image.Tag) {
		return "", fmt.Errorf("invalid release image reference")
	}
	if image.Digest != "" {
		if !digestPattern.MatchString(image.Digest) {
			return "", fmt.Errorf("release digest must be a full lowercase SHA256")
		}
		return image.Repository + "@" + image.Digest, nil
	}
	return image.Repository + ":" + image.Tag, nil
}

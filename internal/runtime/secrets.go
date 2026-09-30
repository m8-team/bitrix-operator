package runtime

import (
	"crypto/sha256"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

type secretSet struct {
	items map[string]corev1.SecretProjection
}

func newSecretSet() *secretSet { return &secretSet{items: map[string]corev1.SecretProjection{}} }
func (s *secretSet) add(ref corev1.SecretKeySelector) (string, error) {
	if len(validation.IsDNS1123Subdomain(ref.Name)) != 0 || len(validation.IsConfigMapKey(ref.Key)) != 0 || ref.Key == "" {
		return "", fmt.Errorf("invalid Secret selector")
	}
	if ref.Optional != nil && *ref.Optional {
		return "", fmt.Errorf("runtime Secrets cannot be optional")
	}
	key := ref.Name + "\x00" + ref.Key
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
	mode := int32(0440)
	optional := false
	s.items[key] = corev1.SecretProjection{LocalObjectReference: corev1.LocalObjectReference{Name: ref.Name},
		Optional: &optional, Items: []corev1.KeyToPath{{Key: ref.Key, Path: digest, Mode: &mode}}}
	return SecretRoot + "/" + digest, nil
}
func (s *secretSet) projections() []corev1.VolumeProjection {
	keys := make([]string, 0, len(s.items))
	for k := range s.items {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	result := make([]corev1.VolumeProjection, 0, len(keys))
	for _, k := range keys {
		v := s.items[k]
		result = append(result, corev1.VolumeProjection{Secret: &v})
	}
	return result
}

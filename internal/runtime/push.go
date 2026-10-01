package runtime

import (
	"encoding/json"
	"fmt"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

func pushSubscriber(endpoints []PushEndpoints) string {
	if len(endpoints) == 1 {
		return endpoints[0].Subscriber
	}
	return ""
}

func renderPush(spec bx.BitrixClusterSpec, refs *secretSet, endpoints []PushEndpoints) (map[string]string, []corev1.VolumeProjection, pushData, error) {
	p := spec.Runtime.Push
	if p == nil || !p.Enabled {
		return nil, nil, pushData{}, nil
	}
	if p.Image == "" || p.Publisher.Replicas < 1 || p.Subscriber.Replicas < 1 {
		return nil, nil, pushData{}, fmt.Errorf("push requires an image and positive publisher/subscriber replicas")
	}
	if len(endpoints) != 1 || len(validation.IsDNS1123Subdomain(endpoints[0].Publisher)) != 0 || len(validation.IsDNS1123Subdomain(endpoints[0].Subscriber)) != 0 {
		return nil, nil, pushData{}, fmt.Errorf("push requires valid publisher/subscriber service names")
	}
	key, err := refs.add(p.SecretRef)
	if err != nil {
		return nil, nil, pushData{}, err
	}
	pushRefs := newSecretSet()
	if _, err = pushRefs.add(p.SecretRef); err != nil {
		return nil, nil, pushData{}, err
	}
	password, err := pushRefs.add(spec.Redis.PasswordSecretRef)
	if err != nil {
		return nil, nil, pushData{}, err
	}
	files := map[string]string{}
	for _, name := range []string{"publisher.json", "subscriber.json", "configure.cjs", "start.sh", "health.cjs"} {
		data, readErr := renderFile(configFiles, "templates/push/"+name, nil)
		if readErr != nil {
			return nil, nil, pushData{}, readErr
		}
		files[name] = data
	}
	data, err := json.Marshal(struct {
		Redis   redisData `json:"redis"`
		KeyFile string    `json:"keyFile"`
	}{redisData{Host: spec.Redis.Host, Port: spec.Redis.Port, PasswordFile: password}, key})
	if err != nil {
		return nil, nil, pushData{}, err
	}
	files["manifest.json"] = string(data)
	return files, pushRefs.projections(), pushData{Enabled: true, Publisher: endpoints[0].Publisher, KeyFile: key}, nil
}

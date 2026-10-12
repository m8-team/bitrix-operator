// Render the actual operator PushPolicy builders for synthetic test identities.
package main

import (
	"encoding/json"
	"os"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"github.com/m8-team/bitrix-operator/internal/workload"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func main() {
	encoder := json.NewEncoder(os.Stdout)
	items := make([]any, 0, 4)
	for _, uid := range []string{"network-a", "network-b"} {
		c := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Name: uid, Namespace: "bitrix-sites", UID: types.UID(uid)}}
		for _, role := range []string{"publisher", "subscriber"} {
			policy := workload.PushPolicy(c, role)
			// No real CR/controller is installed in this focused dataplane test.
			// Remove ownership so GC does not delete the policy; selectors/rules are unchanged.
			policy.OwnerReferences = nil
			items = append(items, policy)
		}
	}
	if err := encoder.Encode(map[string]any{"apiVersion": "v1", "kind": "List", "items": items}); err != nil {
		panic(err)
	}
}

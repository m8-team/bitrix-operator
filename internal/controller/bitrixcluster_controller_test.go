/*
Copyright 2026 M8 Team.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/yaml"

	bitrixv1alpha1 "github.com/m8-team/bitrix-operator/api/v1alpha1"
)

// Load the actual documented sample so it cannot drift away from the schema.
func sample() *unstructured.Unstructured {
	data, err := os.ReadFile("../../config/samples/bitrix_v1alpha1_bitrixcluster.yaml")
	Expect(err).NotTo(HaveOccurred())
	obj := &unstructured.Unstructured{}
	Expect(yaml.Unmarshal(data, &obj.Object)).To(Succeed())
	obj.SetName("")
	obj.SetGenerateName("foundation-")
	obj.SetNamespace("default")
	return obj
}

func createSample() *bitrixv1alpha1.BitrixCluster {
	obj := sample()
	Expect(k8sClient.Create(ctx, obj)).To(Succeed())
	result := &bitrixv1alpha1.BitrixCluster{}
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), result)).To(Succeed())
	DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, result))).To(Succeed()) })
	return result
}

var _ = Describe("BitrixCluster foundation", func() {
	It("records Pending without claiming a running application and is idempotent", func() {
		obj := createSample()
		r := &BitrixClusterReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
		result, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(ctrl.Result{}))
		Expect(k8sClient.Get(ctx, req.NamespacedName, obj)).To(Succeed())
		Expect(obj.Status.Phase).To(Equal(bitrixv1alpha1.BitrixClusterPhasePending))
		Expect(obj.Status.ObservedGeneration).To(Equal(obj.Generation))
		ready := meta.FindStatusCondition(obj.Status.Conditions, "Ready")
		Expect(ready).NotTo(BeNil())
		Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		Expect(ready.Reason).To(Equal("RuntimeNotImplemented"))
		Expect(obj.Status.Release).To(BeNil())
		resourceVersion := obj.ResourceVersion
		transition := ready.LastTransitionTime
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, req.NamespacedName, obj)).To(Succeed())
		Expect(obj.ResourceVersion).To(Equal(resourceVersion))

		obj.Spec.Release.Image.Tag = "next"
		Expect(k8sClient.Update(ctx, obj)).To(Succeed())
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, req.NamespacedName, obj)).To(Succeed())
		Expect(obj.Status.ObservedGeneration).To(Equal(obj.Generation))
		ready = meta.FindStatusCondition(obj.Status.Conditions, "Ready")
		Expect(ready.ObservedGeneration).To(Equal(obj.Generation))
		Expect(ready.LastTransitionTime).To(Equal(transition))
	})

	It("ignores deleted resources", func() {
		r := &BitrixClusterReconciler{Client: k8sClient}
		result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{
			Namespace: "default", Name: "does-not-exist",
		}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(ctrl.Result{}))
	})

	It("starts a real manager and reconciles watch events without Gateway API CRDs", func() {
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme: k8sClient.Scheme(), Metrics: metricsserver.Options{BindAddress: "0"},
			HealthProbeBindAddress: "0", LeaderElection: false,
		})
		Expect(err).NotTo(HaveOccurred())
		r := &BitrixClusterReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}
		Expect(r.SetupWithManager(mgr)).To(Succeed())
		managerCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- mgr.Start(managerCtx) }()
		DeferCleanup(func() {
			stop()
			Eventually(done, 10*time.Second).Should(Receive(BeNil()))
		})
		obj := createSample()
		key := client.ObjectKeyFromObject(obj)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key, obj)).To(Succeed())
			g.Expect(obj.Status.ObservedGeneration).To(Equal(obj.Generation))
			g.Expect(meta.IsStatusConditionFalse(obj.Status.Conditions, "Ready")).To(BeTrue())
		}, 10*time.Second, 50*time.Millisecond).Should(Succeed())
	})
})

var _ = Describe("API server schema and defaults", func() {
	It("accepts the HTTP sample without TLS or optional runtime components and applies defaults", func() {
		obj := createSample()
		Expect(obj.Spec.Sites[0].TLS).To(BeNil())
		Expect(obj.Spec.Networking).To(BeNil())
		Expect(obj.Spec.Runtime.Push).To(BeNil())
		Expect(obj.Spec.Runtime.Cron.Agents).To(BeNil())
		Expect(obj.Spec.Runtime.Web.Autoscaling).To(BeNil())
		Expect(obj.Spec.Runtime.Web.Replicas).To(Equal(int32(1)))
		Expect(string(obj.Spec.Release.Image.PullPolicy)).To(Equal("IfNotPresent"))
		Expect(obj.Spec.Database.Connections[0].Name).To(Equal("default"))
	})

	DescribeTable("rejects invalid input",
		func(mutate func(map[string]any)) {
			obj := sample()
			mutate(obj.Object["spec"].(map[string]any))
			err := k8sClient.Create(ctx, obj)
			if err == nil {
				DeferCleanup(func() { Expect(k8sClient.Delete(ctx, obj)).To(Succeed()) })
			}
			Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected a validation error, got: %v", err)
		},
		Entry("missing required release", func(s map[string]any) { delete(s, "release") }),
		Entry("missing required license", func(s map[string]any) { delete(s, "license") }),
		Entry("invalid database enum", func(s map[string]any) { s["database"].(map[string]any)["engine"] = "SQLite" }),
		Entry("zero database port", func(s map[string]any) { connection(s)["port"] = int64(0) }),
		Entry("missing default connection", func(s map[string]any) { connection(s)["name"] = "primary" }),
		Entry("duplicate default connection", func(s map[string]any) {
			db := s["database"].(map[string]any)
			db["connections"] = append(db["connections"].([]any), connection(s))
		}),
		Entry("invalid pull policy", func(s map[string]any) {
			s["release"].(map[string]any)["image"].(map[string]any)["pullPolicy"] = "Sometimes"
		}),
		Entry("TLS without secretName", func(s map[string]any) {
			s["sites"].([]any)[0].(map[string]any)["tls"] = map[string]any{}
		}),
		Entry("empty sites", func(s map[string]any) { s["sites"] = []any{} }),
		Entry("cron enabled without agents", func(s map[string]any) {
			s["runtime"].(map[string]any)["cron"] = map[string]any{"enabled": true}
		}),
		Entry("gateway enabled without parent", func(s map[string]any) {
			s["networking"] = map[string]any{"gateway": map[string]any{"enabled": true}}
		}),
		Entry("autoscaling reversed bounds", func(s map[string]any) {
			web(s)["autoscaling"] = map[string]any{"enabled": true, "minReplicas": int64(3), "maxReplicas": int64(2)}
		}),
		Entry("autoscaling missing bounds", func(s map[string]any) {
			web(s)["autoscaling"] = map[string]any{"enabled": true}
		}),
		Entry("uploads must be PVC", func(s map[string]any) {
			s["storage"].(map[string]any)["uploads"].(map[string]any)["type"] = "EmptyDir"
		}),
		Entry("temporary must be EmptyDir", func(s map[string]any) {
			s["storage"].(map[string]any)["temporary"].(map[string]any)["type"] = "PersistentVolumeClaim"
		}),
	)

	It("accepts disabled optional objects without their enabled-only fields", func() {
		obj := sample()
		s := obj.Object["spec"].(map[string]any)
		web(s)["autoscaling"] = map[string]any{"enabled": false}
		s["networking"] = map[string]any{"gateway": map[string]any{"enabled": false}}
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, obj)).To(Succeed()) })
	})

	It("round-trips durations, PostgreSQL, connections and explicit optional objects", func() {
		obj := sample()
		s := obj.Object["spec"].(map[string]any)
		s["database"].(map[string]any)["engine"] = "PostgreSQL"
		connection(s)["port"] = int64(5432)
		connection(s)["options"] = map[string]any{"charset": "UTF8", "timezone": "UTC"}
		s["sites"].([]any)[0].(map[string]any)["tls"] = map[string]any{"secretName": "portal-tls"}
		s["runtime"].(map[string]any)["cron"] = map[string]any{
			"enabled": true, "agents": map[string]any{"enabled": true, "interval": "30s"},
		}
		web(s)["autoscaling"] = map[string]any{"enabled": true, "minReplicas": int64(1), "maxReplicas": int64(3)}
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, obj)).To(Succeed()) })
		typed := &bitrixv1alpha1.BitrixCluster{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), typed)).To(Succeed())
		Expect(typed.Spec.Runtime.Cron.Agents.Interval.Duration).To(Equal(30 * time.Second))
		Expect(typed.Spec.Database.Engine).To(Equal(bitrixv1alpha1.DatabaseEnginePostgreSQL))
		Expect(typed.Spec.Sites[0].TLS.SecretName).To(Equal("portal-tls"))
	})

	It("isolates the status subresource from spec and generation", func() {
		obj := createSample()
		obj.Status.Phase = bitrixv1alpha1.BitrixClusterPhaseReady
		Expect(k8sClient.Update(ctx, obj)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj)).To(Succeed())
		Expect(obj.Status.Phase).To(BeEmpty())
		// Typed full updates can normalize optional objects; baseline after that update.
		generation := obj.Generation
		obj.Status.Phase = bitrixv1alpha1.BitrixClusterPhasePending
		obj.Spec.Release.Image.Tag = "must-not-persist"
		Expect(k8sClient.Status().Update(ctx, obj)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj)).To(Succeed())
		Expect(obj.Spec.Release.Image.Tag).To(Equal("2026.09.1"))
		Expect(obj.Status.Phase).To(Equal(bitrixv1alpha1.BitrixClusterPhasePending))
		Expect(obj.Generation).To(Equal(generation))
	})
})

func connection(s map[string]any) map[string]any {
	return s["database"].(map[string]any)["connections"].([]any)[0].(map[string]any)
}

func web(s map[string]any) map[string]any {
	return s["runtime"].(map[string]any)["web"].(map[string]any)
}

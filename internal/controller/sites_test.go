package controller

import (
	"encoding/json"
	"strings"
	"time"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	"github.com/m8-team/bitrix-operator/internal/workload"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const siteTestDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func markWebReady(c *bx.BitrixCluster) {
	p := uploads(c)
	p.Status.Phase = corev1.ClaimBound
	Expect(k8sClient.Status().Update(ctx, p)).To(Succeed())
	d := webDeployment(c)
	d.Status = appsv1.DeploymentStatus{ObservedGeneration: d.Generation, Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1}
	Expect(k8sClient.Status().Update(ctx, d)).To(Succeed())
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{GenerateName: "web-rs-", Namespace: c.Namespace, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(d, appsv1.SchemeGroupVersion.WithKind("Deployment"))}}, Spec: appsv1.ReplicaSetSpec{Selector: d.Spec.Selector, Template: d.Spec.Template}}
	Expect(k8sClient.Create(ctx, rs)).To(Succeed())
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{GenerateName: "web-pod-", Namespace: c.Namespace, Labels: d.Spec.Template.Labels, Annotations: d.Spec.Template.Annotations, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(rs, appsv1.SchemeGroupVersion.WithKind("ReplicaSet"))}}, Spec: d.Spec.Template.Spec}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	pod.Status = corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}, InitContainerStatuses: []corev1.ContainerStatus{{Name: "release", ImageID: siteTestDigest, State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}}}}
	Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
}
func validationJob(c *bx.BitrixCluster) *batchv1.Job {
	jobs := &batchv1.JobList{}
	Expect(k8sClient.List(ctx, jobs, client.InNamespace(c.Namespace))).To(Succeed())
	Expect(jobs.Items).To(HaveLen(1))
	return &jobs.Items[0]
}
func finishValidation(c *bx.BitrixCluster, j *batchv1.Job, reason string, sites []string) *corev1.Pod {
	result := render.SiteResult{Version: render.ContractVersion, Identity: render.ValidationIdentity{ClusterUID: string(c.UID), Generation: c.Generation, Release: siteTestDigest, Attempt: string(j.UID)}, Sites: sites, Reason: reason}
	data, err := json.Marshal(result)
	Expect(err).NotTo(HaveOccurred())
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{GenerateName: "validation-", Namespace: c.Namespace, Labels: j.Spec.Template.Labels, Annotations: j.Spec.Template.Annotations, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(j, batchv1.SchemeGroupVersion.WithKind("Job"))}}, Spec: j.Spec.Template.Spec}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	pod.Status = corev1.PodStatus{Phase: corev1.PodSucceeded, InitContainerStatuses: []corev1.ContainerStatus{{Name: "release", ImageID: siteTestDigest, State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}}}, ContainerStatuses: []corev1.ContainerStatus{{Name: "php", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0, Message: string(data)}}}}}
	Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
	j.Status.Succeeded = 1
	now := metav1.Now()
	j.Status.StartTime = &now
	j.Status.CompletionTime = &now
	j.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobSuccessCriteriaMet, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Now()}, {Type: batchv1.JobComplete, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Now()}}
	Expect(k8sClient.Status().Update(ctx, j)).To(Succeed())
	return pod
}

var _ = Describe("Site validation", func() {
	It("validates the current runtime, applies idempotently and invalidates generation changes", func() {
		c := reconcileWeb(webSample())
		markWebReady(c)
		reconcileWeb(c)
		j := validationJob(c)
		Expect(*j.Spec.ActiveDeadlineSeconds).To(Equal(int64(120)))
		Expect(*j.Spec.BackoffLimit).To(BeZero())
		for _, v := range j.Spec.Template.Spec.Volumes {
			Expect(v.PersistentVolumeClaim).To(BeNil())
		}
		finishValidation(c, j, sitesValidated, []string{"s1"})
		reconcileWeb(c)
		Expect(meta.IsStatusConditionTrue(c.Status.Conditions, "Ready")).To(BeTrue())
		Expect(c.Status.Sites[0].Exists).To(BeTrue())
		Expect(c.Status.Sites[0].URLs).To(BeEmpty())
		version := c.ResourceVersion
		reconcileWeb(c)
		Expect(c.ResourceVersion).To(Equal(version))
		c.Spec.Sites[0].Hosts = []string{"changed.example.com"}
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.IsStatusConditionTrue(c.Status.Conditions, "Ready")).To(BeFalse())
		Expect(c.Status.Sites[0].Exists).To(BeFalse())
	})
	It("reports missing sites without treating the successful Job as Ready", func() {
		c := reconcileWeb(webSample())
		markWebReady(c)
		reconcileWeb(c)
		finishValidation(c, validationJob(c), "SiteNotFound", nil)
		reconcileWeb(c)
		Expect(meta.FindStatusCondition(c.Status.Conditions, "SitesValidated").Reason).To(Equal("SiteNotFound"))
		Expect(c.Status.Phase).To(Equal(bx.BitrixClusterPhaseDegraded))
	})
	It("rejects stale identity, different image content, foreign owners and failed containers", func() {
		c := reconcileWeb(webSample())
		markWebReady(c)
		reconcileWeb(c)
		j := validationJob(c)
		pod := finishValidation(c, j, sitesValidated, []string{"s1"})
		original := pod.DeepCopy()
		pod.Status.ContainerStatuses[0].State.Terminated.Message = strings.ReplaceAll(pod.Status.ContainerStatuses[0].State.Terminated.Message, string(j.UID), "stale")
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.FindStatusCondition(c.Status.Conditions, "SitesValidated").Reason).To(Equal("ValidationUnavailable"))
		pod.Status = original.Status
		pod.Status.InitContainerStatuses[0].ImageID = "sha256:" + strings.Repeat("b", 64)
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.IsStatusConditionTrue(c.Status.Conditions, "Ready")).To(BeFalse())
		pod.Status.InitContainerStatuses[0].ImageID = siteTestDigest
		pod.Status.ContainerStatuses[0].State.Terminated.ExitCode = 1
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.IsStatusConditionTrue(c.Status.Conditions, "Ready")).To(BeFalse())
		pod.Status.ContainerStatuses[0].State.Terminated.ExitCode = 0
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
		pod.OwnerReferences[0].UID = types.UID("foreign")
		Expect(k8sClient.Update(ctx, pod)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.IsStatusConditionTrue(c.Status.Conditions, "Ready")).To(BeFalse())
	})
	It("expires successful results and removes completed attempts", func() {
		c := reconcileWeb(webSample())
		markWebReady(c)
		reconcileWeb(c)
		j := validationJob(c)
		finishValidation(c, j, sitesValidated, []string{"s1"})
		for i := range j.Status.Conditions {
			j.Status.Conditions[i].LastTransitionTime = metav1.NewTime(time.Now().Add(-16 * time.Minute))
		}
		Expect(k8sClient.Status().Update(ctx, j)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.IsStatusConditionTrue(c.Status.Conditions, "Ready")).To(BeFalse())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(j), j)).To(Succeed())
		Expect(j.DeletionTimestamp.IsZero()).To(BeFalse())
	})
	It("does not reuse a successful result after Secret rotation", func() {
		c := reconcileWeb(webSample())
		markWebReady(c)
		reconcileWeb(c)
		finishValidation(c, validationJob(c), sitesValidated, []string{"s1"})
		reconcileWeb(c)
		secret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: c.Spec.Crypto.SecretRef.Name}, secret)).To(Succeed())
		secret.Data[c.Spec.Crypto.SecretRef.Key] = []byte("rotated")
		Expect(k8sClient.Update(ctx, secret)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.IsStatusConditionTrue(c.Status.Conditions, "Ready")).To(BeFalse())
		Expect(c.Status.Sites[0].Exists).To(BeFalse())
		markWebReady(c)
		reconcileWeb(c)
		j := validationJob(c)
		Expect(j.DeletionTimestamp.IsZero()).To(BeFalse())
	})
	It("uses validation labels that cannot enter the web service", func() {
		c := reconcileWeb(webSample())
		markWebReady(c)
		reconcileWeb(c)
		j := validationJob(c)
		Expect(j.Spec.Template.Labels["app.kubernetes.io/name"]).To(Equal("bitrix-sites"))
		Expect(j.Spec.Template.Annotations[workload.ClusterName]).To(Equal(c.Name))
	})
})

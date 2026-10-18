/*
Copyright 2026.

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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DatabaseEngine identifies a database engine supported by the operator.
// +kubebuilder:validation:Enum=MySQL;PostgreSQL
type DatabaseEngine string

const (
	DatabaseEngineMySQL      DatabaseEngine = "MySQL"
	DatabaseEnginePostgreSQL DatabaseEngine = "PostgreSQL"
)

// StorageType identifies the Kubernetes storage implementation used by a
// BitrixCluster storage section.
// +kubebuilder:validation:Enum=PersistentVolumeClaim;EmptyDir
type StorageType string

const (
	StorageTypePersistentVolumeClaim StorageType = "PersistentVolumeClaim"
	StorageTypeEmptyDir              StorageType = "EmptyDir"
)

// BitrixClusterPhase is a high-level, human-readable cluster phase.
// Conditions remain the authoritative source for detailed status.
// +kubebuilder:validation:Enum=Pending;Progressing;Ready;Degraded
type BitrixClusterPhase string

const (
	BitrixClusterPhasePending     BitrixClusterPhase = "Pending"
	BitrixClusterPhaseProgressing BitrixClusterPhase = "Progressing"
	BitrixClusterPhaseReady       BitrixClusterPhase = "Ready"
	BitrixClusterPhaseDegraded    BitrixClusterPhase = "Degraded"
)

// BitrixClusterSpec defines the desired state of BitrixCluster.
type BitrixClusterSpec struct {
	// Release defines the immutable application image deployed by the operator.
	// +required
	Release ReleaseSpec `json:"release"`

	// License references the Secret key containing the Bitrix license value.
	// +required
	License SecretValueSpec `json:"license"`

	// Crypto references the Secret key containing the Bitrix encryption key.
	// +required
	Crypto SecretValueSpec `json:"crypto"`

	// Sites maps existing Bitrix site IDs to externally published hosts.
	// Site existence is validated by the controller against the Bitrix installation.
	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=id
	// +required
	Sites []BitrixSiteSpec `json:"sites"`

	// Runtime configures web, cron, agents, and Push & Pull workloads.
	// +required
	Runtime RuntimeSpec `json:"runtime"`

	// Database configures connections rendered into the Bitrix application settings.
	// +required
	Database DatabaseSpec `json:"database"`

	// Redis configures the shared Redis endpoint used by cache and sessions.
	// +required
	Redis RedisSpec `json:"redis"`

	// Storage configures persistent uploads and temporary runtime storage.
	// +required
	Storage StorageSpec `json:"storage"`

	// Networking optionally configures publication through Kubernetes Gateway API.
	// When omitted, Gateway API resources are not created.
	// +optional
	Networking *NetworkingSpec `json:"networking,omitempty"`
}

// ReleaseSpec defines the application image deployed by the cluster.
type ReleaseSpec struct {
	// Image identifies the immutable Bitrix application release.
	// +required
	Image ReleaseImageSpec `json:"image"`

	// ImagePullSecrets lists Secrets used to pull the release image.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`
}

// ReleaseImageSpec identifies an image by repository, tag, and optionally digest.
type ReleaseImageSpec struct {
	// Repository is the image repository without a tag or digest.
	// +kubebuilder:validation:MinLength=1
	// +required
	Repository string `json:"repository"`

	// Tag is the human-readable release version.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`
	// +required
	Tag string `json:"tag"`

	// Digest pins the image to immutable content. The controller should prefer the
	// digest over the tag when both are specified.
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}/*
Copyright 2026.

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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DatabaseEngine identifies a database engine supported by the operator.
// +kubebuilder:validation:Enum=MySQL;PostgreSQL
type DatabaseEngine string

const (
	DatabaseEngineMySQL      DatabaseEngine = "MySQL"
	DatabaseEnginePostgreSQL DatabaseEngine = "PostgreSQL"
)

// StorageType identifies the Kubernetes storage implementation used by a
// BitrixCluster storage section.
// +kubebuilder:validation:Enum=PersistentVolumeClaim;EmptyDir
type StorageType string

const (
	StorageTypePersistentVolumeClaim StorageType = "PersistentVolumeClaim"
	StorageTypeEmptyDir              StorageType = "EmptyDir"
)

// BitrixClusterPhase is a high-level, human-readable cluster phase.
// Conditions remain the authoritative source for detailed status.
// +kubebuilder:validation:Enum=Pending;Progressing;Ready;Degraded
type BitrixClusterPhase string

const (
	BitrixClusterPhasePending     BitrixClusterPhase = "Pending"
	BitrixClusterPhaseProgressing BitrixClusterPhase = "Progressing"
	BitrixClusterPhaseReady       BitrixClusterPhase = "Ready"
	BitrixClusterPhaseDegraded    BitrixClusterPhase = "Degraded"
)

// BitrixClusterSpec defines the desired state of BitrixCluster.
type BitrixClusterSpec struct {
	// Release defines the immutable application image deployed by the operator.
	// +required
	Release ReleaseSpec `json:"release"`

	// License references the Secret key containing the Bitrix license value.
	// +required
	License SecretValueSpec `json:"license"`

	// Crypto references the Secret key containing the Bitrix encryption key.
	// +required
	Crypto SecretValueSpec `json:"crypto"`

	// Sites maps existing Bitrix site IDs to externally published hosts.
	// Site existence is validated by the controller against the Bitrix installation.
	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=id
	// +required
	Sites []BitrixSiteSpec `json:"sites"`

	// Runtime configures web, cron, agents, and Push & Pull workloads.
	// +required
	Runtime RuntimeSpec `json:"runtime"`

	// Database configures connections rendered into the Bitrix application settings.
	// +required
	Database DatabaseSpec `json:"database"`

	// Redis configures the shared Redis endpoint used by cache and sessions.
	// +required
	Redis RedisSpec `json:"redis"`

	// Storage configures persistent uploads and temporary runtime storage.
	// +required
	Storage StorageSpec `json:"storage"`

	// Networking optionally configures publication through Kubernetes Gateway API.
	// When omitted, Gateway API resources are not created.
	// +optional
	Networking *NetworkingSpec `json:"networking,omitempty"`
}

// ReleaseSpec defines the application image deployed by the cluster.
type ReleaseSpec struct {
	// Image identifies the immutable Bitrix application release.
	// +required
	Image ReleaseImageSpec `json:"image"`

	// ImagePullSecrets lists Secrets used to pull the release image.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`
}

// ReleaseImageSpec identifies an image by repository, tag, and optionally digest.
type ReleaseImageSpec struct {
	// Repository is the image repository without a tag or digest.
	// +kubebuilder:validation:MinLength=1
	// +required
	Repository string `json:"repository"`

	// Tag is the human-readable release version.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`
	// +required
	Tag string `json:"tag"`

	// Digest pins the image to immutable content. The controller should prefer the
	// digest over the tag when both are specified.
	
	// +optional
	Digest string `json:"digest,omitempty"`

	// PullPolicy controls when Kubernetes pulls the image.
	// +kubebuilder:validation:Enum=Always;IfNotPresent;Never
	// +kubebuilder:default=IfNotPresent
	// +optional
	PullPolicy corev1.PullPolicy `json:"pullPolicy,omitempty"`
}

// SecretValueSpec references one value in a namespaced Secret.
type SecretValueSpec struct {
	// SecretRef selects the Secret name and data key.
	// +required
	SecretRef corev1.SecretKeySelector `json:"secretRef"`
}

// BitrixSiteSpec publishes an existing Bitrix site through one or more hosts.
type BitrixSiteSpec struct {
	// ID is the real Bitrix site ID, for example s1 or s2.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_-]+$`
	// +required
	ID string `json:"id"`

	// Hosts contains DNS names routed to this Bitrix site.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:MinLength=1
	// +listType=set
	// +required
	Hosts []string `json:"hosts"`

	// TLS optionally configures the certificate used for HTTPS publication.
	// It may be omitted for HTTP publication. If the selected Gateway listener
	// uses HTTPS, the controller must require TLS to be configured for the site.
	// +optional
	TLS *SiteTLSSpec `json:"tls,omitempty"`
}

// SiteTLSSpec references an existing kubernetes.io/tls Secret.
type SiteTLSSpec struct {
	// SecretName is the name of a TLS Secret in the BitrixCluster namespace.
	// +kubebuilder:validation:MinLength=1
	// +required
	SecretName string `json:"secretName"`
}

// RuntimeSpec configures all Bitrix runtime workloads.
type RuntimeSpec struct {
	// Web configures the Nginx and PHP-FPM web workload.
	// +required
	Web WebRuntimeSpec `json:"web"`

	// Cron configures the singleton cron and Bitrix agents workload.
	// +required
	Cron CronRuntimeSpec `json:"cron"`

	// Push configures optional Bitrix Push & Pull publisher and subscriber workloads.
	// When omitted, Push & Pull is disabled.
	// +optional
	Push *PushRuntimeSpec `json:"push,omitempty"`
}

// WebRuntimeSpec configures the horizontally scalable web workload.
type WebRuntimeSpec struct {
	// Replicas is the desired number of web Pods when autoscaling is disabled.
	// It is also used as the initial replica count when autoscaling is enabled.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// Autoscaling optionally configures a HorizontalPodAutoscaler for the web workload.
	// When omitted, the web workload uses the fixed replica count.
	// +optional
	Autoscaling *AutoscalingSpec `json:"autoscaling,omitempty"`

	// Nginx configures the Nginx container.
	// +required
	Nginx ContainerRuntimeSpec `json:"nginx"`

	// PHP configures the PHP-FPM container.
	// +required
	PHP PHPRuntimeSpec `json:"php"`
}

// AutoscalingSpec configures web workload autoscaling.
// +kubebuilder:validation:XValidation:rule="!self.enabled || self.minReplicas >= 1",message="minReplicas must be at least 1 when autoscaling is enabled"
// +kubebuilder:validation:XValidation:rule="!self.enabled || self.maxReplicas >= self.minReplicas",message="maxReplicas must be greater than or equal to minReplicas"
type AutoscalingSpec struct {
	// Enabled controls creation of the HorizontalPodAutoscaler.
	// +required
	Enabled bool `json:"enabled"`

	// MinReplicas is the lower autoscaling boundary.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MinReplicas int32 `json:"minReplicas,omitempty"`

	// MaxReplicas is the upper autoscaling boundary.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxReplicas int32 `json:"maxReplicas,omitempty"`

	// TargetCPUUtilization is the average CPU utilization target in percent.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +optional
	TargetCPUUtilization *int32 `json:"targetCPUUtilization,omitempty"`

	// TargetMemoryUtilization is the average memory utilization target in percent.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +optional
	TargetMemoryUtilization *int32 `json:"targetMemoryUtilization,omitempty"`
}

// ContainerRuntimeSpec configures an application container image and resources.
type ContainerRuntimeSpec struct {
	// Image is a complete OCI image reference.
	// +kubebuilder:validation:MinLength=1
	// +required
	Image string `json:"image"`

	// Resources specifies compute resources for the container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// PHPRuntimeSpec configures the PHP-FPM container.
type PHPRuntimeSpec struct {
	// Image is a complete OCI image reference.
	// +kubebuilder:validation:MinLength=1
	// +required
	Image string `json:"image"`

	// Resources specifies compute resources for the PHP-FPM container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// CronRuntimeSpec configures the singleton cron workload.
// When enabled, the operator always creates exactly one cron replica.
// +kubebuilder:validation:XValidation:rule="!self.enabled || has(self.agents)",message="agents is required when cron is enabled"
type CronRuntimeSpec struct {
	// Enabled controls creation of the singleton cron workload.
	// +required
	Enabled bool `json:"enabled"`

	// Agents configures execution of Bitrix agents by cron.
	// It may be omitted when cron is disabled.
	// +optional
	Agents *AgentsSpec `json:"agents,omitempty"`
}

// AgentsSpec configures periodic execution of Bitrix agents.
type AgentsSpec struct {
	// Enabled controls periodic Bitrix agent execution.
	// +required
	Enabled bool `json:"enabled"`

	// Interval is the interval between agent executions.
	// +required
	Interval metav1.Duration `json:"interval"`
}

// PushRuntimeSpec configures Bitrix Push & Pull workloads.
type PushRuntimeSpec struct {
	// Enabled controls creation of Push & Pull workloads.
	// +required
	Enabled bool `json:"enabled"`

	// Image is the Push & Pull OCI image.
	// +kubebuilder:validation:MinLength=1
	// +required
	Image string `json:"image"`

	// SecretRef selects the Push & Pull shared secret.
	// +required
	SecretRef corev1.SecretKeySelector `json:"secretRef"`

	// Publisher configures Push & Pull publisher replicas.
	// +required
	Publisher ReplicaSpec `json:"publisher"`

	// Subscriber configures Push & Pull subscriber replicas.
	// +required
	Subscriber ReplicaSpec `json:"subscriber"`
}

// ReplicaSpec configures a workload replica count.
type ReplicaSpec struct {
	// Replicas is the desired number of replicas.
	// +kubebuilder:validation:Minimum=1
	// +required
	Replicas int32 `json:"replicas"`
}

// DatabaseSpec configures Bitrix database connections.
// The connection named "default" is used as the primary Bitrix connection.
// +kubebuilder:validation:XValidation:rule="self.connections.filter(c, c.name == 'default').size() == 1",message="exactly one database connection must be named default"
type DatabaseSpec struct {
	// Engine is the database engine shared by the configured connections.
	// +required
	Engine DatabaseEngine `json:"engine"`

	// Connections is the set of named Bitrix database connections.
	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=name
	// +required
	Connections []DatabaseConnectionSpec `json:"connections"`
}

// DatabaseConnectionSpec configures one named Bitrix database connection.
type DatabaseConnectionSpec struct {
	// Name is the connection name used in Bitrix settings.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+$`
	// +required
	Name string `json:"name"`

	// Host is the database DNS name or IP address.
	// +kubebuilder:validation:MinLength=1
	// +required
	Host string `json:"host"`

	// Port is the database TCP port.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +required
	Port int32 `json:"port"`

	// Database is the database name.
	// +kubebuilder:validation:MinLength=1
	// +required
	Database string `json:"database"`

	// Username is the database user name.
	// +kubebuilder:validation:MinLength=1
	// +required
	Username string `json:"username"`

	// PasswordSecretRef selects the database password.
	// +required
	PasswordSecretRef corev1.SecretKeySelector `json:"passwordSecretRef"`

	// Options contains database-specific Bitrix connection options.
	// +optional
	Options DatabaseOptionsSpec `json:"options,omitempty"`
}

// DatabaseOptionsSpec contains database connection options rendered for Bitrix.
type DatabaseOptionsSpec struct {
	// Charset configures the database connection charset.
	// +optional
	Charset string `json:"charset,omitempty"`

	// Timezone configures the database session timezone.
	// +optional
	Timezone string `json:"timezone,omitempty"`
}

// RedisSpec configures the shared Redis endpoint.
type RedisSpec struct {
	// Host is the Redis DNS name or IP address.
	// +kubebuilder:validation:MinLength=1
	// +required
	Host string `json:"host"`

	// Port is the Redis TCP port.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +required
	Port int32 `json:"port"`

	// PasswordSecretRef selects the Redis password.
	// +required
	PasswordSecretRef corev1.SecretKeySelector `json:"passwordSecretRef"`

	// Cache configures Redis-backed Bitrix cache.
	// +required
	Cache RedisCacheSpec `json:"cache"`

	// Sessions configures Redis-backed PHP sessions.
	// +required
	Sessions RedisSessionsSpec `json:"sessions"`
}

// RedisCacheSpec configures Redis-backed Bitrix cache.
type RedisCacheSpec struct {
	// SID is the unique cache namespace identifier used by Bitrix.
	// +kubebuilder:validation:MinLength=1
	// +required
	SID string `json:"sid"`
}

// RedisSessionsSpec configures Redis-backed PHP sessions.
type RedisSessionsSpec struct {
	// Mode configures the Bitrix session storage mode.
	// +kubebuilder:validation:Enum=default;separated
	// +required
	Mode string `json:"mode"`

	// TTL is the session lifetime in seconds.
	// +kubebuilder:validation:Minimum=1
	// +required
	TTL int64 `json:"ttl"`
}

// StorageSpec configures uploads and temporary storage.
type StorageSpec struct {
	// Uploads configures persistent user-uploaded files.
	// +required
	Uploads UploadStorageSpec `json:"uploads"`

	// Temporary configures non-persistent runtime files.
	// +required
	Temporary TemporaryStorageSpec `json:"temporary"`
}

// UploadStorageSpec configures uploads storage.
// +kubebuilder:validation:XValidation:rule="self.type == 'PersistentVolumeClaim'",message="uploads storage type must be PersistentVolumeClaim"
type UploadStorageSpec struct {
	// Type identifies the storage implementation.
	// +required
	Type StorageType `json:"type"`

	// MountPath is the uploads directory inside application containers.
	// +kubebuilder:validation:Pattern=`^/([^/]+(/[^/]+)*)?$`
	// +required
	MountPath string `json:"mountPath"`

	// PersistentVolumeClaim configures the PVC created for uploads.
	// +required
	PersistentVolumeClaim PersistentVolumeClaimSpec `json:"persistentVolumeClaim"`
}

// PersistentVolumeClaimSpec describes a PVC managed by the operator.
type PersistentVolumeClaimSpec struct {
	// StorageClassName is the Kubernetes StorageClass used by the PVC.
	// +kubebuilder:validation:MinLength=1
	// +required
	StorageClassName string `json:"storageClassName"`

	// AccessModes contains the requested PersistentVolume access modes.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:Enum=ReadWriteOnce;ReadOnlyMany;ReadWriteMany;ReadWriteOncePod
	// +listType=set
	// +required
	AccessModes []corev1.PersistentVolumeAccessMode `json:"accessModes"`

	// Size is the requested storage capacity.
	// +required
	Size resource.Quantity `json:"size"`
}

// TemporaryStorageSpec configures an EmptyDir volume.
// +kubebuilder:validation:XValidation:rule="self.type == 'EmptyDir'",message="temporary storage type must be EmptyDir"
type TemporaryStorageSpec struct {
	// Type identifies the storage implementation.
	// +required
	Type StorageType `json:"type"`

	// MountPath is the temporary directory inside application containers.
	// +kubebuilder:validation:Pattern=`^/([^/]+(/[^/]+)*)?$`
	// +required
	MountPath string `json:"mountPath"`

	// SizeLimit limits the EmptyDir volume capacity.
	// +required
	SizeLimit resource.Quantity `json:"sizeLimit"`
}

// NetworkingSpec configures cluster networking.
type NetworkingSpec struct {
	// Gateway optionally configures publication through Kubernetes Gateway API.
	// +optional
	Gateway *GatewaySpec `json:"gateway,omitempty"`
}

// GatewaySpec configures attachment to a Gateway API Gateway listener.
// +kubebuilder:validation:XValidation:rule="!self.enabled || has(self.parentRef)",message="parentRef is required when gateway is enabled"
type GatewaySpec struct {
	// Enabled controls creation of HTTPRoute resources.
	// +required
	Enabled bool `json:"enabled"`

	// ParentRef identifies the Gateway and listener used by generated routes.
	// It may be omitted when Gateway API publication is disabled.
	// +optional
	ParentRef *GatewayParentReference `json:"parentRef,omitempty"`
}

// GatewayParentReference identifies a Gateway API parent.
type GatewayParentReference struct {
	// Name is the Gateway resource name.
	// +kubebuilder:validation:MinLength=1
	// +required
	Name string `json:"name"`

	// Namespace is the Gateway resource namespace.
	// +kubebuilder:validation:MinLength=1
	// +required
	Namespace string `json:"namespace"`

	// SectionName selects a listener on the Gateway.
	// +kubebuilder:validation:MinLength=1
	// +required
	SectionName string `json:"sectionName"`
}

// BitrixClusterStatus defines the observed state of BitrixCluster.
type BitrixClusterStatus struct {
	// ObservedGeneration is the most recent metadata.generation reconciled by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Phase is a concise summary for human-readable output.
	// +optional
	Phase BitrixClusterPhase `json:"phase,omitempty"`

	// Release describes the image currently observed by the controller.
	// +optional
	Release *ReleaseStatus `json:"release,omitempty"`

	// Sites contains validation and endpoint status for configured Bitrix sites.
	// +listType=map
	// +listMapKey=id
	// +optional
	Sites []BitrixSiteStatus `json:"sites,omitempty"`

	// Runtime contains rollout status for generated workloads.
	// +optional
	Runtime *RuntimeStatus `json:"runtime,omitempty"`

	// Conditions represent the latest available observations of the resource state.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ReleaseStatus identifies the release image observed by the controller.
type ReleaseStatus struct {
	// Repository is the observed image repository.
	// +optional
	Repository string `json:"repository,omitempty"`

	// Tag is the observed image tag.
	// +optional
	Tag string `json:"tag,omitempty"`

	// Digest is the resolved or configured image digest.
	// +optional
	Digest string `json:"digest,omitempty"`
}

// BitrixSiteStatus contains the observed state of one configured Bitrix site.
type BitrixSiteStatus struct {
	// ID is the Bitrix site ID from spec.sites.
	// +required
	ID string `json:"id"`

	// Exists reports whether the site was found in the Bitrix installation.
	// +optional
	Exists bool `json:"exists,omitempty"`

	// URLs contains externally published site URLs.
	// +listType=set
	// +optional
	URLs []string `json:"urls,omitempty"`

	// Message contains a human-readable site validation result.
	// +optional
	Message string `json:"message,omitempty"`
}

// RuntimeStatus contains observed workload status.
type RuntimeStatus struct {
	// Web is the observed web workload status.
	// +optional
	Web WorkloadStatus `json:"web,omitempty"`

	// Cron is the observed cron workload status.
	// +optional
	Cron WorkloadStatus `json:"cron,omitempty"`

	// PushPublisher is the observed Push & Pull publisher status.
	// +optional
	PushPublisher WorkloadStatus `json:"pushPublisher,omitempty"`

	// PushSubscriber is the observed Push & Pull subscriber status.
	// +optional
	PushSubscriber WorkloadStatus `json:"pushSubscriber,omitempty"`
}

// WorkloadStatus contains common Deployment rollout counters.
type WorkloadStatus struct {
	// DesiredReplicas is the desired number of replicas.
	// +optional
	DesiredReplicas int32 `json:"desiredReplicas,omitempty"`

	// ReadyReplicas is the number of ready replicas.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// UpdatedReplicas is the number of replicas running the desired release.
	// +optional
	UpdatedReplicas int32 `json:"updatedReplicas,omitempty"`

	// AvailableReplicas is the number of available replicas.
	// +optional
	AvailableReplicas int32 `json:"availableReplicas,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=bxcluster;bxcl,categories=all
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Tag",type=string,JSONPath=".spec.release.image.tag"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// BitrixCluster is the Schema for the bitrixclusters API.
type BitrixCluster struct {
	metav1.TypeMeta `json:",inline"`

	// Metadata is standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Spec defines the desired state of BitrixCluster.
	// +required
	Spec BitrixClusterSpec `json:"spec"`

	// Status defines the observed state of BitrixCluster.
	// +optional
	Status BitrixClusterStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// BitrixClusterList contains a list of BitrixCluster.
type BitrixClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []BitrixCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BitrixCluster{}, &BitrixClusterList{})
}

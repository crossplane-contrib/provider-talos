/*
Copyright 2025 The Crossplane Authors.

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
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
)

// ConfigurationApplyParameters are the configurable fields of a ConfigurationApply.
type ConfigurationApplyParameters struct {
	// Node is the target machine identifier (required)
	Node string `json:"node"`
	// Endpoint is the machine endpoint (optional)
	// +optional
	Endpoint *string `json:"endpoint,omitempty"`
	// ApplyMode is the configuration application mode (optional)
	// +optional
	// +kubebuilder:validation:Enum=auto;reboot;no_reboot;staged
	ApplyMode *string `json:"applyMode,omitempty"`
	// MachineConfiguration defines the Talos machine configuration to apply
	MachineConfiguration MachineConfigurationSpec `json:"machineConfiguration"`
	// ConfigPatches is a list of configuration modifications (optional)
	// +optional
	ConfigPatches []string `json:"configPatches,omitempty"`
	// OnDestroy configuration for machine reset during destruction (optional)
	// +optional
	OnDestroy *string `json:"onDestroy,omitempty"`
	// ClientConfiguration for authentication
	ClientConfiguration ClientConfiguration `json:"clientConfiguration"`
}

// ConfigurationApplyObservation are the observable fields of a ConfigurationApply.
type ConfigurationApplyObservation struct {
	// Applied indicates if the configuration was successfully applied
	Applied bool `json:"applied,omitempty"`
	// LastAppliedTime is the timestamp of the last successful application
	LastAppliedTime *metav1.Time `json:"lastAppliedTime,omitempty"`
}

// A ConfigurationApplySpec defines the desired state of a ConfigurationApply.
type ConfigurationApplySpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       ConfigurationApplyParameters `json:"forProvider"`
}

// A ConfigurationApplyStatus represents the observed state of a ConfigurationApply.
type ConfigurationApplyStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          ConfigurationApplyObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A ConfigurationApply applies machine configuration to Talos nodes.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,talos}
type ConfigurationApply struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConfigurationApplySpec   `json:"spec"`
	Status ConfigurationApplyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ConfigurationApplyList contains a list of ConfigurationApply
type ConfigurationApplyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ConfigurationApply `json:"items"`
}

// ConfigurationApply type metadata.
var (
	ConfigurationApplyKind             = reflect.TypeOf(ConfigurationApply{}).Name()
	ConfigurationApplyGroupKind        = schema.GroupKind{Group: Group, Kind: ConfigurationApplyKind}.String()
	ConfigurationApplyKindAPIVersion   = ConfigurationApplyKind + "." + SchemeGroupVersion.String()
	ConfigurationApplyGroupVersionKind = SchemeGroupVersion.WithKind(ConfigurationApplyKind)
)

// MachineConfigurationSpec defines the structure for Talos machine configuration
type MachineConfigurationSpec struct {
	// Version is the Talos configuration version (e.g., v1alpha1)
	Version string `json:"version"`

	// Machine configuration
	Machine MachineSpec `json:"machine"`

	// Cluster configuration
	Cluster ClusterSpec `json:"cluster"`
}

// MachineSpec defines machine-specific configuration
type MachineSpec struct {
	// Type is the machine type (controlplane, worker)
	// +kubebuilder:validation:Enum=controlplane;worker
	Type string `json:"type"`

	// Token for machine authentication
	Token string `json:"token"`

	// Install configuration for the machine
	Install InstallSpec `json:"install"`

	// Network configuration (optional)
	// +optional
	Network *NetworkSpec `json:"network,omitempty"`

	// Kubelet configuration (optional)
	// +optional
	Kubelet *KubeletSpec `json:"kubelet,omitempty"`

	// Features configuration (optional)
	// +optional
	Features *FeaturesSpec `json:"features,omitempty"`

	// CA defines the certificate authority configuration (optional)
	// +optional
	CA *CASpec `json:"ca,omitempty"`
}

// ClusterSpec defines cluster-specific configuration
type ClusterSpec struct {
	// ID is the cluster unique identifier
	ID string `json:"id"`

	// Secret is the cluster shared secret
	Secret string `json:"secret"`

	// ClusterName is the name of the cluster
	ClusterName string `json:"clusterName"`

	// ControlPlane defines control plane configuration
	ControlPlane ControlPlaneSpec `json:"controlPlane"`

	// Network defines cluster networking
	Network ClusterNetworkSpec `json:"network"`

	// Token for cluster bootstrap
	Token string `json:"token"`
}

// InstallSpec defines installation configuration
type InstallSpec struct {
	// Disk is the target disk for installation
	Disk string `json:"disk"`

	// Image is the Talos installer image
	Image string `json:"image"`

	// Wipe indicates whether to wipe the disk
	// +optional
	Wipe *bool `json:"wipe,omitempty"`
}

// ControlPlaneSpec defines control plane configuration
type ControlPlaneSpec struct {
	// Endpoint is the control plane endpoint URL
	Endpoint string `json:"endpoint"`
}

// ClusterNetworkSpec defines cluster networking
type ClusterNetworkSpec struct {
	// DNSDomain is the cluster DNS domain
	// +optional
	DNSDomain *string `json:"dnsDomain,omitempty"`

	// PodSubnets are the pod network CIDRs
	// +optional
	PodSubnets []string `json:"podSubnets,omitempty"`

	// ServiceSubnets are the service network CIDRs
	// +optional
	ServiceSubnets []string `json:"serviceSubnets,omitempty"`
}

// NetworkSpec defines machine network configuration (optional fields)
type NetworkSpec struct {
	// Additional network configuration can be added here
}

// KubeletSpec defines kubelet configuration (optional fields)
type KubeletSpec struct {
	// Image is the kubelet image
	// +optional
	Image *string `json:"image,omitempty"`
}

// FeaturesSpec defines feature configuration (optional fields)
type FeaturesSpec struct {
	// RBAC enables role-based access control
	// +optional
	RBAC *bool `json:"rbac,omitempty"`
}

// CASpec defines certificate authority configuration
type CASpec struct {
	// Crt is the PEM-encoded certificate
	Crt string `json:"crt"`

	// Key is the PEM-encoded private key
	Key string `json:"key"`
}

func init() {
	SchemeBuilder.Register(&ConfigurationApply{}, &ConfigurationApplyList{})
}

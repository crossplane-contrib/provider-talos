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

// ConfigurationParameters are the configurable fields of a Configuration.
type ConfigurationParameters struct {
	// Node is the Talos node endpoint for configuration management (required)
	Node string `json:"node"`
	// ClusterName is the Kubernetes cluster name (required)
	ClusterName string `json:"clusterName"`
	// MachineType is the machine type: control plane or worker (required)
	// +kubebuilder:validation:Enum=controlplane;worker
	MachineType string `json:"machineType"`
	// ClusterEndpoint is the Kubernetes API endpoint (required)
	ClusterEndpoint string `json:"clusterEndpoint"`
	// MachineSecretsRef references the machine secrets
	MachineSecretsRef *xpv1.Reference `json:"machineSecretsRef,omitempty"`
	// TalosVersion is the Talos version (optional)
	// +optional
	TalosVersion *string `json:"talosVersion,omitempty"`
	// KubernetesVersion is the Kubernetes version (optional)
	// +optional
	KubernetesVersion *string `json:"kubernetesVersion,omitempty"`
	// ConfigPatches are configuration modifications (optional)
	// +optional
	ConfigPatches []string `json:"configPatches,omitempty"`
}

// ConfigurationObservation are the observable fields of a Configuration.
type ConfigurationObservation struct {
	// MachineConfiguration is the generated Talos configuration
	MachineConfiguration string `json:"machineConfiguration,omitempty"`
	// GeneratedTime is when the configuration was generated
	GeneratedTime *metav1.Time `json:"generatedTime,omitempty"`
}

// A ConfigurationSpec defines the desired state of a Configuration.
type ConfigurationSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       ConfigurationParameters `json:"forProvider"`
}

// A ConfigurationStatus represents the observed state of a Configuration.
type ConfigurationStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          ConfigurationObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A Configuration generates Talos machine configurations.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,talos}
type Configuration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConfigurationSpec   `json:"spec"`
	Status ConfigurationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ConfigurationList contains a list of Configuration
type ConfigurationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Configuration `json:"items"`
}

// Configuration type metadata.
var (
	ConfigurationKind             = reflect.TypeOf(Configuration{}).Name()
	ConfigurationGroupKind        = schema.GroupKind{Group: Group, Kind: ConfigurationKind}.String()
	ConfigurationKindAPIVersion   = ConfigurationKind + "." + SchemeGroupVersion.String()
	ConfigurationGroupVersionKind = SchemeGroupVersion.WithKind(ConfigurationKind)
)

func init() {
	SchemeBuilder.Register(&Configuration{}, &ConfigurationList{})
}

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
	// MachineConfigurationInput is the configuration to apply (required, sensitive)
	MachineConfigurationInput string `json:"machineConfigurationInput"`
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

func init() {
	SchemeBuilder.Register(&ConfigurationApply{}, &ConfigurationApplyList{})
}

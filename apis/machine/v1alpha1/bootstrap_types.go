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

// BootstrapParameters are the configurable fields of a Bootstrap.
type BootstrapParameters struct {
	// Node is the node to bootstrap (required)
	Node string `json:"node"`
	// Endpoint is the machine endpoint (optional)
	// +optional
	Endpoint *string `json:"endpoint,omitempty"`
	// ClientConfiguration for authentication
	ClientConfiguration ClientConfiguration `json:"clientConfiguration"`
}

// BootstrapObservation are the observable fields of a Bootstrap.
type BootstrapObservation struct {
	// Bootstrapped indicates if the node was successfully bootstrapped
	Bootstrapped bool `json:"bootstrapped,omitempty"`
	// BootstrapTime is the timestamp when bootstrap completed
	BootstrapTime *metav1.Time `json:"bootstrapTime,omitempty"`
}

// A BootstrapSpec defines the desired state of a Bootstrap.
type BootstrapSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       BootstrapParameters `json:"forProvider"`
}

// A BootstrapStatus represents the observed state of a Bootstrap.
type BootstrapStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          BootstrapObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A Bootstrap bootstraps Talos nodes to initialize the cluster.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,talos}
type Bootstrap struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BootstrapSpec   `json:"spec"`
	Status BootstrapStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BootstrapList contains a list of Bootstrap
type BootstrapList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Bootstrap `json:"items"`
}

// Bootstrap type metadata.
var (
	BootstrapKind             = reflect.TypeOf(Bootstrap{}).Name()
	BootstrapGroupKind        = schema.GroupKind{Group: Group, Kind: BootstrapKind}.String()
	BootstrapKindAPIVersion   = BootstrapKind + "." + SchemeGroupVersion.String()
	BootstrapGroupVersionKind = SchemeGroupVersion.WithKind(BootstrapKind)
)

func init() {
	SchemeBuilder.Register(&Bootstrap{}, &BootstrapList{})
}

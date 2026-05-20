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

// ClusterHealthParameters are the configurable fields of a ClusterHealth.
type ClusterHealthParameters struct {
	// Endpoints are Talos API endpoints used by the health check client.
	// Use at least one reachable control-plane endpoint.
	// +kubebuilder:validation:MinItems=1
	Endpoints []string `json:"endpoints"`

	// ControlPlaneNodes are the control-plane nodes to check.
	// +kubebuilder:validation:MinItems=1
	ControlPlaneNodes []string `json:"controlPlaneNodes"`

	// WorkerNodes are optional worker nodes to check.
	// +optional
	WorkerNodes []string `json:"workerNodes,omitempty"`

	// SkipKubernetesChecks skips Kubernetes component checks and only waits for
	// Talos/node-level health.
	// +optional
	SkipKubernetesChecks *bool `json:"skipKubernetesChecks,omitempty"`

	// ClientConfiguration contains Talos client credentials.
	ClientConfiguration ClientConfiguration `json:"clientConfiguration"`
}

// ClusterHealthObservation are the observable fields of a ClusterHealth.
type ClusterHealthObservation struct {
	// Healthy indicates the last health check passed.
	Healthy bool `json:"healthy,omitempty"`

	// LastCheckTime is the last time a health check was attempted.
	// +optional
	LastCheckTime *metav1.Time `json:"lastCheckTime,omitempty"`

	// LastHealthyTime is the last time all requested checks passed.
	// +optional
	LastHealthyTime *metav1.Time `json:"lastHealthyTime,omitempty"`

	// LastMessage summarizes the last health check result.
	// +optional
	LastMessage string `json:"lastMessage,omitempty"`

	// CheckedControlPlaneNodes is the number of control-plane nodes included in the last check.
	// +optional
	CheckedControlPlaneNodes int `json:"checkedControlPlaneNodes,omitempty"`

	// CheckedWorkerNodes is the number of worker nodes included in the last check.
	// +optional
	CheckedWorkerNodes int `json:"checkedWorkerNodes,omitempty"`
}

// A ClusterHealthSpec defines the desired state of a ClusterHealth.
type ClusterHealthSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       ClusterHealthParameters `json:"forProvider"`
}

// A ClusterHealthStatus represents the observed state of a ClusterHealth.
type ClusterHealthStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          ClusterHealthObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A ClusterHealth waits for a Talos cluster to become healthy.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="HEALTHY",type="boolean",JSONPath=".status.atProvider.healthy"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,talos}
type ClusterHealth struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterHealthSpec   `json:"spec"`
	Status ClusterHealthStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterHealthList contains a list of ClusterHealth.
type ClusterHealthList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterHealth `json:"items"`
}

// ClusterHealth type metadata.
var (
	ClusterHealthKind             = reflect.TypeOf(ClusterHealth{}).Name()
	ClusterHealthGroupKind        = schema.GroupKind{Group: Group, Kind: ClusterHealthKind}.String()
	ClusterHealthKindAPIVersion   = ClusterHealthKind + "." + SchemeGroupVersion.String()
	ClusterHealthGroupVersionKind = SchemeGroupVersion.WithKind(ClusterHealthKind)
)

func init() {
	SchemeBuilder.Register(&ClusterHealth{}, &ClusterHealthList{})
}

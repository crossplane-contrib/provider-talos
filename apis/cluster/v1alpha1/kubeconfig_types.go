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

// ClientConfiguration contains client configuration for Talos API
type ClientConfiguration struct {
	// CACertificate is the CA certificate for the cluster
	CACertificate string `json:"caCertificate"`
	// ClientCertificate is the client certificate for authentication
	ClientCertificate string `json:"clientCertificate"`
	// ClientKey is the client private key for authentication
	ClientKey string `json:"clientKey"`
}

// KubeconfigParameters are the configurable fields of a Kubeconfig.
type KubeconfigParameters struct {
	// Node is the control plane node (required)
	Node string `json:"node"`
	// Endpoint is the machine endpoint (optional)
	// +optional
	Endpoint *string `json:"endpoint,omitempty"`
	// ClientConfiguration for authentication
	ClientConfiguration ClientConfiguration `json:"clientConfiguration"`
}

// KubernetesClientConfiguration contains Kubernetes client configuration
type KubernetesClientConfiguration struct {
	// Host is the Kubernetes API server host
	Host string `json:"host"`
	// CACertificate is the cluster CA certificate
	CACertificate string `json:"caCertificate"`
	// ClientCertificate is the client certificate
	ClientCertificate string `json:"clientCertificate"`
	// ClientKey is the client private key
	ClientKey string `json:"clientKey"`
}

// KubeconfigObservation are the observable fields of a Kubeconfig.
type KubeconfigObservation struct {
	// KubernetesClientConfiguration contains the kubeconfig data
	KubernetesClientConfiguration *KubernetesClientConfiguration `json:"kubernetesClientConfiguration,omitempty"`
}

// A KubeconfigSpec defines the desired state of a Kubeconfig.
type KubeconfigSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       KubeconfigParameters `json:"forProvider"`
}

// A KubeconfigStatus represents the observed state of a Kubeconfig.
type KubeconfigStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          KubeconfigObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A Kubeconfig manages Kubernetes configuration access for Talos clusters.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,talos}
type Kubeconfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KubeconfigSpec   `json:"spec"`
	Status KubeconfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// KubeconfigList contains a list of Kubeconfig
type KubeconfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Kubeconfig `json:"items"`
}

// Kubeconfig type metadata.
var (
	KubeconfigKind             = reflect.TypeOf(Kubeconfig{}).Name()
	KubeconfigGroupKind        = schema.GroupKind{Group: Group, Kind: KubeconfigKind}.String()
	KubeconfigKindAPIVersion   = KubeconfigKind + "." + SchemeGroupVersion.String()
	KubeconfigGroupVersionKind = SchemeGroupVersion.WithKind(KubeconfigKind)
)

func init() {
	SchemeBuilder.Register(&Kubeconfig{}, &KubeconfigList{})
}

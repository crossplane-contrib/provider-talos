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

// SecretsParameters are the configurable fields of a Secrets.
type SecretsParameters struct {
	// Node is the Talos node endpoint for secrets validation (optional)
	// +optional
	Node *string `json:"node,omitempty"`
	// TalosVersion is the Talos version for feature compatibility
	// +optional
	TalosVersion *string `json:"talosVersion,omitempty"`
}

// ClientConfiguration contains client configuration for Talos API
type ClientConfiguration struct {
	// CACertificate is the CA certificate for the cluster
	CACertificate string `json:"caCertificate"`
	// ClientCertificate is the client certificate for authentication
	ClientCertificate string `json:"clientCertificate"`
	// ClientKey is the client private key for authentication
	ClientKey string `json:"clientKey"`
}

// MachineSecretsData contains the generated machine secrets
type MachineSecretsData struct {
	// ClusterSecrets contains cluster-wide secrets in JSON format
	ClusterSecrets string `json:"clusterSecrets,omitempty"`
	// KubernetesSecrets contains Kubernetes-specific secrets in JSON format
	KubernetesSecrets string `json:"kubernetesSecrets,omitempty"`
	// TrustdInfo contains TrustD configuration in JSON format
	TrustdInfo string `json:"trustdInfo,omitempty"`
}

// SecretsObservation are the observable fields of a Secrets.
type SecretsObservation struct {
	// MachineSecrets contains the generated secrets structure
	MachineSecrets *MachineSecretsData `json:"machineSecrets,omitempty"`
	// ClientConfiguration contains client configuration for API access
	ClientConfiguration *ClientConfiguration `json:"clientConfiguration,omitempty"`
}

// A SecretsSpec defines the desired state of a Secrets.
type SecretsSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       SecretsParameters `json:"forProvider"`
}

// A SecretsStatus represents the observed state of a Secrets.
type SecretsStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          SecretsObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A Secrets generates and manages machine secrets for Talos clusters.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,talos}
type Secrets struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SecretsSpec   `json:"spec"`
	Status SecretsStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SecretsList contains a list of Secrets
type SecretsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Secrets `json:"items"`
}

// Secrets type metadata.
var (
	SecretsKind             = reflect.TypeOf(Secrets{}).Name()
	SecretsGroupKind        = schema.GroupKind{Group: Group, Kind: SecretsKind}.String()
	SecretsKindAPIVersion   = SecretsKind + "." + SchemeGroupVersion.String()
	SecretsGroupVersionKind = SchemeGroupVersion.WithKind(SecretsKind)
)

func init() {
	SchemeBuilder.Register(&Secrets{}, &SecretsList{})
}

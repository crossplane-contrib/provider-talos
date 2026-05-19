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

// MachineSecrets contains the complete structured Talos machine secrets contract.
type MachineSecrets struct {
	// Cluster contains cluster-wide secrets.
	Cluster MachineSecretsCluster `json:"cluster"`
	// Secrets contains encryption and bootstrap secrets.
	Secrets MachineSecretsSecrets `json:"secrets"`
	// TrustdInfo contains trustd credentials.
	TrustdInfo MachineSecretsTrustdInfo `json:"trustdinfo"`
	// Certs contains Talos and Kubernetes CA material.
	Certs MachineSecretsCerts `json:"certs"`
}

// MachineSecretsCluster contains cluster-wide secrets.
type MachineSecretsCluster struct {
	// ID is the Talos cluster ID.
	ID string `json:"id"`
	// Secret is the Talos cluster secret.
	Secret string `json:"secret"`
}

// MachineSecretsSecrets contains encryption and bootstrap secrets.
type MachineSecretsSecrets struct {
	// BootstrapToken is the bootstrap token.
	BootstrapToken string `json:"bootstrap_token"`
	// SecretboxEncryptionSecret is the secretbox encryption secret.
	SecretboxEncryptionSecret string `json:"secretbox_encryption_secret,omitempty"`
	// AESCBCEncryptionSecret is the AES-CBC encryption secret.
	AESCBCEncryptionSecret string `json:"aescbc_encryption_secret,omitempty"`
}

// MachineSecretsTrustdInfo contains trustd credentials.
type MachineSecretsTrustdInfo struct {
	// Token is the trustd token.
	Token string `json:"token"`
}

// MachineSecretsCerts contains Talos and Kubernetes CA material.
type MachineSecretsCerts struct {
	// Etcd contains etcd CA certificate and key.
	Etcd MachineSecretsCertificateAndKey `json:"etcd"`
	// K8s contains Kubernetes CA certificate and key.
	K8s MachineSecretsCertificateAndKey `json:"k8s"`
	// K8sAggregator contains Kubernetes aggregator CA certificate and key.
	K8sAggregator MachineSecretsCertificateAndKey `json:"k8s_aggregator"`
	// K8sServiceAccount contains Kubernetes service account key.
	K8sServiceAccount MachineSecretsKey `json:"k8s_serviceaccount"`
	// OS contains Talos API CA certificate and key.
	OS MachineSecretsCertificateAndKey `json:"os"`
}

// MachineSecretsCertificateAndKey contains a base64-encoded PEM certificate and key.
type MachineSecretsCertificateAndKey struct {
	// Cert is a base64-encoded PEM certificate.
	Cert string `json:"cert"`
	// Key is a base64-encoded PEM private key.
	Key string `json:"key"`
}

// MachineSecretsKey contains a base64-encoded PEM key.
type MachineSecretsKey struct {
	// Key is a base64-encoded PEM private key.
	Key string `json:"key"`
}

// SecretsObservation are the observable fields of a Secrets.
type SecretsObservation struct {
	// Generated indicates machine secrets have been generated and published to connection details.
	Generated bool `json:"generated,omitempty"`
	// GeneratedTime is when machine secrets were first generated.
	// +optional
	GeneratedTime *metav1.Time `json:"generatedTime,omitempty"`
	// MachineSecretsHash is a SHA-256 hash of the machine secrets bundle connection detail.
	// +optional
	MachineSecretsHash string `json:"machineSecretsHash,omitempty"`
	// ClientConfigurationHash is a SHA-256 hash of the client configuration connection detail.
	// +optional
	ClientConfigurationHash string `json:"clientConfigurationHash,omitempty"`
	// TalosConfigHash is a SHA-256 hash of the talos config connection detail.
	// +optional
	TalosConfigHash string `json:"talosConfigHash,omitempty"`
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

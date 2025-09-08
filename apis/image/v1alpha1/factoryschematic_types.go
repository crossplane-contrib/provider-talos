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

// FactorySchematicParameters are the configurable fields of a FactorySchematic.
type FactorySchematicParameters struct {
	// Schematic is the YAML configuration for image customization (optional)
	// +optional
	Schematic *string `json:"schematic,omitempty"`
}

// FactorySchematicObservation are the observable fields of a FactorySchematic.
type FactorySchematicObservation struct {
	// ID is the unique schematic identifier
	ID string `json:"id,omitempty"`
}

// A FactorySchematicSpec defines the desired state of a FactorySchematic.
type FactorySchematicSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       FactorySchematicParameters `json:"forProvider"`
}

// A FactorySchematicStatus represents the observed state of a FactorySchematic.
type FactorySchematicStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          FactorySchematicObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A FactorySchematic creates custom Talos image schematics through the Image Factory.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,talos}
type FactorySchematic struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FactorySchematicSpec   `json:"spec"`
	Status FactorySchematicStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// FactorySchematicList contains a list of FactorySchematic
type FactorySchematicList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FactorySchematic `json:"items"`
}

// FactorySchematic type metadata.
var (
	FactorySchematicKind             = reflect.TypeOf(FactorySchematic{}).Name()
	FactorySchematicGroupKind        = schema.GroupKind{Group: Group, Kind: FactorySchematicKind}.String()
	FactorySchematicKindAPIVersion   = FactorySchematicKind + "." + SchemeGroupVersion.String()
	FactorySchematicGroupVersionKind = SchemeGroupVersion.WithKind(FactorySchematicKind)
)

func init() {
	SchemeBuilder.Register(&FactorySchematic{}, &FactorySchematicList{})
}

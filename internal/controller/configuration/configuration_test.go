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

package configuration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	talossecrets "github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/crossplane/crossplane-runtime/pkg/test"

	machinev1alpha1 "github.com/crossplane-contrib/provider-talos/apis/machine/v1alpha1"
	secretscontroller "github.com/crossplane-contrib/provider-talos/internal/controller/secrets"
)

// Unlike many Kubernetes projects Crossplane does not use third party testing
// libraries, per the common Go test review comments. Crossplane encourages the
// use of table driven unit tests. The tests of the crossplane-runtime project
// are representative of the testing style Crossplane encourages.
//
// https://github.com/golang/go/wiki/TestComments
// https://github.com/crossplane/crossplane/blob/master/CONTRIBUTING.md#contributing-code

func TestObserve(t *testing.T) {
	type fields struct {
		service *TalosConfigurationService
	}

	type args struct {
		ctx context.Context
		mg  resource.Managed
	}

	type want struct {
		o   managed.ExternalObservation
		err error
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		// TODO: Add test cases.
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := external{service: tc.fields.service}
			got, err := e.Observe(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Observe(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
			if diff := cmp.Diff(tc.want.o, got); diff != "" {
				t.Errorf("\n%s\ne.Observe(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestObservePublishesMachineConfiguration(t *testing.T) {
	t.Parallel()

	bundle, err := talossecrets.NewBundle(talossecrets.NewClock(), nil)
	if err != nil {
		t.Fatalf("talossecrets.NewBundle(...): %v", err)
	}
	bundleJSON, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("json.Marshal(...): %v", err)
	}
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("corev1.AddToScheme(...): %v", err)
	}
	if err := machinev1alpha1.SchemeBuilder.AddToScheme(scheme); err != nil {
		t.Fatalf("machinev1alpha1.SchemeBuilder.AddToScheme(...): %v", err)
	}

	connectionSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "example-machine-secrets-connection", Namespace: "default"}, Data: map[string][]byte{
		connectionKeyMachineSecretsBundle: bundleJSON,
	}}
	machineSecrets := &machinev1alpha1.Secrets{
		ObjectMeta: metav1.ObjectMeta{Name: "example-machine-secrets"},
		Spec:       machinev1alpha1.SecretsSpec{ResourceSpec: xpv1.ResourceSpec{WriteConnectionSecretToReference: &xpv1.SecretReference{Name: connectionSecret.Name, Namespace: connectionSecret.Namespace}}},
	}

	configuration := &machinev1alpha1.Configuration{
		ObjectMeta: metav1.ObjectMeta{Name: "example-config"},
		Spec: machinev1alpha1.ConfigurationSpec{ForProvider: machinev1alpha1.ConfigurationParameters{
			ClusterName:       "example-cluster",
			ClusterEndpoint:   "https://10.0.0.1:6443",
			MachineType:       "controlplane",
			MachineSecretsRef: &xpv1.Reference{Name: machineSecrets.Name},
			ConfigPatches: []string{`machine:
  nodeLabels:
    environment: production`},
		}},
	}

	e := external{kube: fake.NewClientBuilder().WithScheme(scheme).WithObjects(machineSecrets, connectionSecret).Build()}
	got, err := e.Observe(context.Background(), configuration)
	if err != nil {
		t.Fatalf("e.Observe(...): %v", err)
	}

	machineConfig := string(got.ConnectionDetails[connectionKeyMachineConfiguration])
	if machineConfig == "" {
		t.Fatal("expected machine configuration connection detail")
	}
	for _, want := range []string{"clusterName: example-cluster", "type: controlplane", "environment: production", "etcd:", "aggregatorCA:"} {
		if !strings.Contains(machineConfig, want) {
			t.Fatalf("expected generated machine configuration to contain %q", want)
		}
	}
	if configuration.Status.AtProvider.MachineConfiguration != machineConfig {
		t.Fatal("expected status machine configuration to match connection detail")
	}
	if configuration.Status.AtProvider.MachineConfigurationHash == "" {
		t.Fatal("expected machine configuration hash in status")
	}
	if configuration.Status.AtProvider.GeneratedTime == nil {
		t.Fatal("expected generated time in status")
	}
	if !got.ResourceExists || !got.ResourceUpToDate {
		t.Fatalf("expected existing and up to date resource, got %+v", got)
	}
}

func TestGetMachineSecretsBundleFallsBackToRawBundle(t *testing.T) {
	t.Parallel()

	bundle, err := talossecrets.NewBundle(talossecrets.NewClock(), nil)
	if err != nil {
		t.Fatalf("talossecrets.NewBundle(...): %v", err)
	}
	bundleJSON, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("json.Marshal(...): %v", err)
	}

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("corev1.AddToScheme(...): %v", err)
	}
	if err := machinev1alpha1.SchemeBuilder.AddToScheme(scheme); err != nil {
		t.Fatalf("machinev1alpha1.SchemeBuilder.AddToScheme(...): %v", err)
	}

	connectionSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "example-machine-secrets-connection", Namespace: "default"}, Data: map[string][]byte{
		connectionKeyMachineSecretsBundle: bundleJSON,
	}}
	machineSecrets := &machinev1alpha1.Secrets{
		ObjectMeta: metav1.ObjectMeta{Name: "example-machine-secrets"},
		Spec:       machinev1alpha1.SecretsSpec{ResourceSpec: xpv1.ResourceSpec{WriteConnectionSecretToReference: &xpv1.SecretReference{Name: connectionSecret.Name, Namespace: connectionSecret.Namespace}}},
	}

	configuration := &machinev1alpha1.Configuration{Spec: machinev1alpha1.ConfigurationSpec{ForProvider: machinev1alpha1.ConfigurationParameters{
		MachineSecretsRef: &xpv1.Reference{Name: machineSecrets.Name},
	}}}

	e := external{kube: fake.NewClientBuilder().WithScheme(scheme).WithObjects(machineSecrets, connectionSecret).Build()}
	got, err := e.getMachineSecretsBundle(context.Background(), configuration)
	if err != nil {
		t.Fatalf("e.getMachineSecretsBundle(...): %v", err)
	}
	if got.Cluster.ID != bundle.Cluster.ID {
		t.Fatalf("expected cluster ID %q, got %q", bundle.Cluster.ID, got.Cluster.ID)
	}
}

func TestGetMachineSecretsBundleFallsBackToStructuredSecret(t *testing.T) {
	t.Parallel()

	bundle, err := talossecrets.NewBundle(talossecrets.NewClock(), nil)
	if err != nil {
		t.Fatalf("talossecrets.NewBundle(...): %v", err)
	}
	machineSecretsData, err := secretscontroller.SecretsBundleToMachineSecrets(bundle)
	if err != nil {
		t.Fatalf("secretscontroller.SecretsBundleToMachineSecrets(...): %v", err)
	}
	structuredJSON, err := json.Marshal(machineSecretsData)
	if err != nil {
		t.Fatalf("json.Marshal(...): %v", err)
	}

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("corev1.AddToScheme(...): %v", err)
	}
	if err := machinev1alpha1.SchemeBuilder.AddToScheme(scheme); err != nil {
		t.Fatalf("machinev1alpha1.SchemeBuilder.AddToScheme(...): %v", err)
	}

	connectionSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "example-machine-secrets-connection", Namespace: "default"}, Data: map[string][]byte{
		connectionKeyMachineSecrets: structuredJSON,
	}}
	machineSecrets := &machinev1alpha1.Secrets{
		ObjectMeta: metav1.ObjectMeta{Name: "example-machine-secrets"},
		Spec:       machinev1alpha1.SecretsSpec{ResourceSpec: xpv1.ResourceSpec{WriteConnectionSecretToReference: &xpv1.SecretReference{Name: connectionSecret.Name, Namespace: connectionSecret.Namespace}}},
	}
	configuration := &machinev1alpha1.Configuration{Spec: machinev1alpha1.ConfigurationSpec{ForProvider: machinev1alpha1.ConfigurationParameters{
		MachineSecretsRef: &xpv1.Reference{Name: machineSecrets.Name},
	}}}

	e := external{kube: fake.NewClientBuilder().WithScheme(scheme).WithObjects(machineSecrets, connectionSecret).Build()}
	got, err := e.getMachineSecretsBundle(context.Background(), configuration)
	if err != nil {
		t.Fatalf("e.getMachineSecretsBundle(...): %v", err)
	}
	if got.Cluster.ID != bundle.Cluster.ID {
		t.Fatalf("expected cluster ID %q, got %q", bundle.Cluster.ID, got.Cluster.ID)
	}
}

func TestGetMachineSecretsBundleRequiresConnectionSecretRef(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := machinev1alpha1.SchemeBuilder.AddToScheme(scheme); err != nil {
		t.Fatalf("machinev1alpha1.SchemeBuilder.AddToScheme(...): %v", err)
	}
	machineSecrets := &machinev1alpha1.Secrets{ObjectMeta: metav1.ObjectMeta{Name: "example-machine-secrets"}}
	configuration := &machinev1alpha1.Configuration{Spec: machinev1alpha1.ConfigurationSpec{ForProvider: machinev1alpha1.ConfigurationParameters{
		MachineSecretsRef: &xpv1.Reference{Name: machineSecrets.Name},
	}}}

	_, err := (&external{kube: fake.NewClientBuilder().WithScheme(scheme).WithObjects(machineSecrets).Build()}).getMachineSecretsBundle(context.Background(), configuration)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "writeConnectionSecretToRef") {
		t.Fatalf("expected writeConnectionSecretToRef error, got %v", err)
	}
}

func TestObserveRequiresMachineSecretsRef(t *testing.T) {
	t.Parallel()

	configuration := &machinev1alpha1.Configuration{
		ObjectMeta: metav1.ObjectMeta{Name: "example-config"},
		Spec: machinev1alpha1.ConfigurationSpec{ForProvider: machinev1alpha1.ConfigurationParameters{
			ClusterName:     "example-cluster",
			ClusterEndpoint: "https://10.0.0.1:6443",
			MachineType:     "worker",
		}},
	}

	_, err := (&external{}).Observe(context.Background(), configuration)
	if err == nil {
		t.Fatal("e.Observe(...): expected error")
	}
	if !strings.Contains(err.Error(), "machineSecretsRef is required") {
		t.Fatalf("e.Observe(...): expected machineSecretsRef required error, got %v", err)
	}
}

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

package secrets

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"testing"
	"time"

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
		service *TalosSecretsService
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

func TestSecretsBundleToMachineSecrets(t *testing.T) {
	t.Parallel()

	bundle, err := talossecrets.NewBundle(talossecrets.NewClock(), nil)
	if err != nil {
		t.Fatalf("talossecrets.NewBundle(...): %v", err)
	}

	machineSecrets, err := SecretsBundleToMachineSecrets(bundle)
	if err != nil {
		t.Fatalf("SecretsBundleToMachineSecrets(...): %v", err)
	}

	if machineSecrets.Cluster.ID != bundle.Cluster.ID {
		t.Fatalf("expected cluster ID %q, got %q", bundle.Cluster.ID, machineSecrets.Cluster.ID)
	}
	if machineSecrets.Secrets.BootstrapToken != bundle.Secrets.BootstrapToken {
		t.Fatalf("expected bootstrap token %q, got %q", bundle.Secrets.BootstrapToken, machineSecrets.Secrets.BootstrapToken)
	}
	if machineSecrets.TrustdInfo.Token != bundle.TrustdInfo.Token {
		t.Fatalf("expected trustd token %q, got %q", bundle.TrustdInfo.Token, machineSecrets.TrustdInfo.Token)
	}

	for name, value := range map[string]string{
		"certs.etcd.cert":              machineSecrets.Certs.Etcd.Cert,
		"certs.etcd.key":               machineSecrets.Certs.Etcd.Key,
		"certs.k8s.cert":               machineSecrets.Certs.K8s.Cert,
		"certs.k8s.key":                machineSecrets.Certs.K8s.Key,
		"certs.k8s_aggregator.cert":    machineSecrets.Certs.K8sAggregator.Cert,
		"certs.k8s_aggregator.key":     machineSecrets.Certs.K8sAggregator.Key,
		"certs.k8s_serviceaccount.key": machineSecrets.Certs.K8sServiceAccount.Key,
		"certs.os.cert":                machineSecrets.Certs.OS.Cert,
		"certs.os.key":                 machineSecrets.Certs.OS.Key,
	} {
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			t.Fatalf("expected %s to be base64 encoded: %v", name, err)
		}
		if block, _ := pem.Decode(decoded); block == nil {
			t.Fatalf("expected %s to decode to PEM", name)
		}
	}
}

func TestMachineSecretsToSecretsBundle(t *testing.T) {
	t.Parallel()

	bundle, err := talossecrets.NewBundle(talossecrets.NewClock(), nil)
	if err != nil {
		t.Fatalf("talossecrets.NewBundle(...): %v", err)
	}
	machineSecrets, err := SecretsBundleToMachineSecrets(bundle)
	if err != nil {
		t.Fatalf("SecretsBundleToMachineSecrets(...): %v", err)
	}

	got, err := MachineSecretsToSecretsBundle(machineSecrets)
	if err != nil {
		t.Fatalf("MachineSecretsToSecretsBundle(...): %v", err)
	}

	if got.Cluster.ID != bundle.Cluster.ID || got.Cluster.Secret != bundle.Cluster.Secret {
		t.Fatal("expected cluster fields to round trip")
	}
	if !bytes.Equal(got.Certs.OS.Crt, bundle.Certs.OS.Crt) || !bytes.Equal(got.Certs.OS.Key, bundle.Certs.OS.Key) {
		t.Fatal("expected OS CA to round trip")
	}
}

func TestGenerateClientConfiguration(t *testing.T) {
	t.Parallel()

	bundle, err := talossecrets.NewBundle(talossecrets.NewClock(), nil)
	if err != nil {
		t.Fatalf("talossecrets.NewBundle(...): %v", err)
	}

	clientConfiguration, err := GenerateClientConfiguration(bundle, time.Hour)
	if err != nil {
		t.Fatalf("GenerateClientConfiguration(...): %v", err)
	}

	if clientConfiguration.ClientCertificate == string(bundle.Certs.OS.Crt) {
		t.Fatal("expected client certificate to differ from OS CA certificate")
	}
	if _, err := tls.X509KeyPair([]byte(clientConfiguration.ClientCertificate), []byte(clientConfiguration.ClientKey)); err != nil {
		t.Fatalf("expected client certificate and key to match: %v", err)
	}

	ca := parseCertificate(t, []byte(clientConfiguration.CACertificate))
	client := parseCertificate(t, []byte(clientConfiguration.ClientCertificate))
	if err := client.CheckSignatureFrom(ca); err != nil {
		t.Fatalf("expected client certificate to be signed by OS CA: %v", err)
	}
}

func TestGenerateMachineSecretsUsesAdminClientCertificate(t *testing.T) {
	t.Parallel()

	generated, err := (&external{}).generateMachineSecrets(nil)
	if err != nil {
		t.Fatalf("generateMachineSecrets(...): unexpected error: %v", err)
	}

	clientConfiguration := generated.ClientConfiguration
	if clientConfiguration == nil {
		t.Fatal("expected client configuration")
	}
	if clientConfiguration.ClientCertificate == clientConfiguration.CACertificate {
		t.Fatal("ClientCertificate matches CACertificate, want generated leaf certificate")
	}

	clientCert := parseCertificate(t, []byte(clientConfiguration.ClientCertificate))
	if clientCert.IsCA {
		t.Fatal("ClientCertificate IsCA = true, want false")
	}
	if diff := cmp.Diff([]string{"os:admin"}, clientCert.Subject.Organization); diff != "" {
		t.Errorf("ClientCertificate Subject.Organization: -want, +got:\n%s", diff)
	}
	if !hasClientAuthUsage(clientCert) {
		t.Fatal("ClientCertificate missing ExtKeyUsageClientAuth")
	}

	caCert := parseCertificate(t, []byte(clientConfiguration.CACertificate))
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	if _, err := clientCert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("ClientCertificate did not verify against CACertificate: %v", err)
	}

	details, err := connectionDetailsFromGeneratedSecrets(generated)
	if err != nil {
		t.Fatalf("connectionDetailsFromGeneratedSecrets(...): %v", err)
	}

	var talosConfig struct {
		Contexts map[string]struct {
			CA  string `json:"ca"`
			Crt string `json:"crt"`
			Key string `json:"key"`
		} `json:"contexts"`
	}
	if err := json.Unmarshal(details[connectionKeyTalosConfig], &talosConfig); err != nil {
		t.Fatalf("TalosConfig JSON unmarshal failed: %v", err)
	}
	ctx, ok := talosConfig.Contexts["default"]
	if !ok {
		t.Fatal("TalosConfig missing default context")
	}
	if diff := cmp.Diff(clientConfiguration.CACertificate, ctx.CA); diff != "" {
		t.Errorf("TalosConfig CA: -want, +got:\n%s", diff)
	}
	if diff := cmp.Diff(clientConfiguration.ClientCertificate, ctx.Crt); diff != "" {
		t.Errorf("TalosConfig client certificate: -want, +got:\n%s", diff)
	}
	if diff := cmp.Diff(clientConfiguration.ClientKey, ctx.Key); diff != "" {
		t.Errorf("TalosConfig client key: -want, +got:\n%s", diff)
	}
}

func TestConnectionDetailsFromGeneratedSecrets(t *testing.T) {
	t.Parallel()

	bundle, err := talossecrets.NewBundle(talossecrets.NewClock(), nil)
	if err != nil {
		t.Fatalf("talossecrets.NewBundle(...): %v", err)
	}
	machineSecrets, err := SecretsBundleToMachineSecrets(bundle)
	if err != nil {
		t.Fatalf("SecretsBundleToMachineSecrets(...): %v", err)
	}
	clientConfiguration, err := GenerateClientConfiguration(bundle, time.Hour)
	if err != nil {
		t.Fatalf("GenerateClientConfiguration(...): %v", err)
	}
	bundleJSON, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("json.Marshal(...): %v", err)
	}

	details, err := connectionDetailsFromGeneratedSecrets(&GeneratedSecretsResult{
		Bundle:              string(bundleJSON),
		MachineSecrets:      machineSecrets,
		ClientConfiguration: clientConfiguration,
	})
	if err != nil {
		t.Fatalf("connectionDetailsFromGeneratedSecrets(...): %v", err)
	}

	if bytes.Equal(details[connectionKeyMachineSecrets], bundleJSON) {
		t.Fatal("expected machine_secrets to contain structured JSON, not raw bundle JSON")
	}
	if !bytes.Equal(details[connectionKeyMachineSecretsBundle], bundleJSON) {
		t.Fatal("expected machine_secrets_bundle to contain raw bundle JSON")
	}
	if string(details[connectionKeyClientCertificate]) != clientConfiguration.ClientCertificate {
		t.Fatal("expected top-level client_certificate to remain raw PEM")
	}
	if !json.Valid(details[connectionKeyTalosConfig]) {
		t.Fatal("expected talos_config to contain JSON")
	}

	var connectionClientConfiguration machinev1alpha1.ClientConfiguration
	if err := json.Unmarshal(details[connectionKeyClientConfiguration], &connectionClientConfiguration); err != nil {
		t.Fatalf("json.Unmarshal(...): %v", err)
	}
	decodedClientCertificate, err := base64.StdEncoding.DecodeString(connectionClientConfiguration.ClientCertificate)
	if err != nil {
		t.Fatalf("expected client_configuration.clientCertificate to be base64: %v", err)
	}
	if string(decodedClientCertificate) != clientConfiguration.ClientCertificate {
		t.Fatal("expected client_configuration.clientCertificate to decode to raw PEM")
	}
}

func TestObserveWritesMetadataOnlyStatus(t *testing.T) {
	t.Parallel()

	cr := &machinev1alpha1.Secrets{
		ObjectMeta: metav1.ObjectMeta{Name: "example-secrets"},
		Spec: machinev1alpha1.SecretsSpec{
			ResourceSpec: xpv1.ResourceSpec{WriteConnectionSecretToReference: &xpv1.SecretReference{Name: "example-connection", Namespace: "default"}},
		},
	}

	got, err := (&external{}).Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe(...): %v", err)
	}
	if !got.ResourceExists || !got.ResourceUpToDate {
		t.Fatalf("expected existing and up to date resource, got %+v", got)
	}
	if !cr.Status.AtProvider.Generated {
		t.Fatal("expected generated status metadata")
	}
	if cr.Status.AtProvider.GeneratedTime == nil {
		t.Fatal("expected generated time")
	}
	if cr.Status.AtProvider.MachineSecretsHash == "" || cr.Status.AtProvider.ClientConfigurationHash == "" || cr.Status.AtProvider.TalosConfigHash == "" {
		t.Fatal("expected status hashes")
	}

	statusJSON, err := json.Marshal(cr.Status.AtProvider)
	if err != nil {
		t.Fatalf("json.Marshal(...): %v", err)
	}
	for _, forbidden := range []string{"BEGIN PRIVATE KEY", "bootstrap_token", "trustdinfo"} {
		if bytes.Contains(statusJSON, []byte(forbidden)) {
			t.Fatalf("status contains sensitive marker %q: %s", forbidden, statusJSON)
		}
	}
	if len(got.ConnectionDetails) == 0 {
		t.Fatal("expected generated connection details")
	}
}

func TestObserveLoadsConnectionDetailsFromSecret(t *testing.T) {
	t.Parallel()

	generated, err := (&external{}).generateMachineSecrets(nil)
	if err != nil {
		t.Fatalf("generateMachineSecrets(...): %v", err)
	}
	details, err := connectionDetailsFromGeneratedSecrets(generated)
	if err != nil {
		t.Fatalf("connectionDetailsFromGeneratedSecrets(...): %v", err)
	}

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("corev1.AddToScheme(...): %v", err)
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "example-connection", Namespace: "default"}, Data: map[string][]byte(details)}
	cr := &machinev1alpha1.Secrets{
		ObjectMeta: metav1.ObjectMeta{Name: "example-secrets"},
		Spec:       machinev1alpha1.SecretsSpec{ResourceSpec: xpv1.ResourceSpec{WriteConnectionSecretToReference: &xpv1.SecretReference{Name: secret.Name, Namespace: secret.Namespace}}},
		Status:     machinev1alpha1.SecretsStatus{AtProvider: machinev1alpha1.SecretsObservation{Generated: true}},
	}

	got, err := (&external{kube: fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()}).Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe(...): %v", err)
	}
	if diff := cmp.Diff(details, got.ConnectionDetails); diff != "" {
		t.Errorf("ConnectionDetails: -want, +got:\n%s", diff)
	}
}

func parseCertificate(t *testing.T, pemBytes []byte) *x509.Certificate {
	t.Helper()

	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatal("expected PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("x509.ParseCertificate(...): %v", err)
	}

	return cert
}

func hasClientAuthUsage(cert *x509.Certificate) bool {
	for _, usage := range cert.ExtKeyUsage {
		if usage == x509.ExtKeyUsageClientAuth {
			return true
		}
	}
	return false
}

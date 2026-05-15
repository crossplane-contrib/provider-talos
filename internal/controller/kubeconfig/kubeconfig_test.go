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

package kubeconfig

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"

	"github.com/crossplane-contrib/provider-talos/apis/cluster/v1alpha1"
	machinev1alpha1 "github.com/crossplane-contrib/provider-talos/apis/machine/v1alpha1"
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
		kube    ctrlclient.Client
		service interface{}
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
		"NotKubeconfig": {
			reason: "Observe should reject managed resources with the wrong type.",
			args: args{
				ctx: context.Background(),
				mg:  &machinev1alpha1.Bootstrap{},
			},
			want: want{
				err: errorsNew(errNotKubeconfig),
			},
		},
		"NotRetrieved": {
			reason: "Observe should report a missing resource until durable kubeconfig data is published.",
			args: args{
				ctx: context.Background(),
				mg:  &v1alpha1.Kubeconfig{},
			},
			want: want{
				o: managed.ExternalObservation{
					ResourceExists:    false,
					ResourceUpToDate:  false,
					ConnectionDetails: managed.ConnectionDetails{},
				},
			},
		},
		"RetrievedFromConnectionSecret": {
			reason: "Observe should report published kubeconfig data as existing and up to date.",
			fields: fields{
				kube: testKubeClient(t, &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "kubeconfig", Namespace: "default"},
					Data:       map[string][]byte{connectionKeyKubeconfig: []byte(testKubeconfig())},
				}),
			},
			args: args{
				ctx: context.Background(),
				mg: &v1alpha1.Kubeconfig{
					Spec: v1alpha1.KubeconfigSpec{
						ResourceSpec: xpv1.ResourceSpec{WriteConnectionSecretToReference: &xpv1.SecretReference{Name: "kubeconfig", Namespace: "default"}},
					},
				},
			},
			want: want{
				o: managed.ExternalObservation{
					ResourceExists:    true,
					ResourceUpToDate:  true,
					ConnectionDetails: connectionDetails(testKubeconfig(), testClientConfiguration()),
				},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := external{kube: tc.fields.kube, service: tc.fields.service}
			got, err := e.Observe(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Observe(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
			if diff := cmp.Diff(tc.want.o, got); diff != "" {
				t.Errorf("\n%s\ne.Observe(...): -want, +got:\n%s\n", tc.reason, diff)
			}

			if cr, ok := tc.args.mg.(*v1alpha1.Kubeconfig); ok && tc.want.o.ResourceExists {
				if got := cr.GetCondition(xpv1.TypeReady).Status; got != "True" {
					t.Errorf("\n%s\ne.Observe(...): want Ready=True, got %s\n", tc.reason, got)
				}
			}
		})
	}
}

func TestObserveAfterCreateStatusDiscarded(t *testing.T) {
	kubeconfigData := testKubeconfig()
	cr := &v1alpha1.Kubeconfig{Spec: v1alpha1.KubeconfigSpec{
		ResourceSpec: xpv1.ResourceSpec{WriteConnectionSecretToReference: &xpv1.SecretReference{Name: "kubeconfig", Namespace: "default"}},
	}}
	e := external{retrieveKubeconfigFn: func(_ context.Context, _ *v1alpha1.Kubeconfig) (string, error) {
		return kubeconfigData, nil
	}}

	created, err := e.Create(context.Background(), cr)
	if err != nil {
		t.Fatalf("e.Create(...): unexpected error: %v", err)
	}

	observedCR := cr.DeepCopy()
	observedCR.Status.AtProvider.KubernetesClientConfiguration = nil
	observedCR.SetConditions()
	e.kube = testKubeClient(t, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "kubeconfig", Namespace: "default"},
		Data:       map[string][]byte{connectionKeyKubeconfig: created.ConnectionDetails[connectionKeyKubeconfig]},
	})

	got, err := e.Observe(context.Background(), observedCR)
	if err != nil {
		t.Fatalf("e.Observe(...): unexpected error: %v", err)
	}
	if !got.ResourceExists || !got.ResourceUpToDate {
		t.Fatalf("e.Observe(...): got %+v, want existing and up to date", got)
	}
	if got := observedCR.GetCondition(xpv1.TypeReady).Status; got != "True" {
		t.Errorf("e.Observe(...): want Ready=True, got %s", got)
	}
	if diff := cmp.Diff(testClientConfiguration(), observedCR.Status.AtProvider.KubernetesClientConfiguration); diff != "" {
		t.Errorf("e.Observe(...): -want status, +got status:\n%s", diff)
	}
	if diff := cmp.Diff(connectionDetails(kubeconfigData, testClientConfiguration()), got.ConnectionDetails); diff != "" {
		t.Errorf("e.Observe(...): -want connection details, +got connection details:\n%s", diff)
	}
}

func TestCreate(t *testing.T) {
	kubeconfigData := testKubeconfig()
	cr := &v1alpha1.Kubeconfig{}
	e := external{retrieveKubeconfigFn: func(_ context.Context, _ *v1alpha1.Kubeconfig) (string, error) {
		return kubeconfigData, nil
	}}

	got, err := e.Create(context.Background(), cr)
	if err != nil {
		t.Fatalf("e.Create(...): unexpected error: %v", err)
	}

	if cr.Status.AtProvider.KubernetesClientConfiguration != nil {
		t.Errorf("e.Create(...): status = %+v, want nil", cr.Status.AtProvider.KubernetesClientConfiguration)
	}
	wantConfiguration := testClientConfiguration()
	if diff := cmp.Diff(connectionDetails(kubeconfigData, wantConfiguration), got.ConnectionDetails); diff != "" {
		t.Errorf("e.Create(...): -want connection details, +got connection details:\n%s", diff)
	}
}

func TestUpdate(t *testing.T) {
	kubeconfigData := testKubeconfig()
	cr := &v1alpha1.Kubeconfig{}
	e := external{retrieveKubeconfigFn: func(_ context.Context, _ *v1alpha1.Kubeconfig) (string, error) {
		return kubeconfigData, nil
	}}

	got, err := e.Update(context.Background(), cr)
	if err != nil {
		t.Fatalf("e.Update(...): unexpected error: %v", err)
	}

	if cr.Status.AtProvider.KubernetesClientConfiguration != nil {
		t.Errorf("e.Update(...): status = %+v, want nil", cr.Status.AtProvider.KubernetesClientConfiguration)
	}
	wantConfiguration := testClientConfiguration()
	if diff := cmp.Diff(connectionDetails(kubeconfigData, wantConfiguration), got.ConnectionDetails); diff != "" {
		t.Errorf("e.Update(...): -want connection details, +got connection details:\n%s", diff)
	}
}

func TestGetKubeconfigEndpoint(t *testing.T) {
	emptyEndpoint := ""
	customEndpoint := "10.0.0.5:50000"

	tests := map[string]struct {
		cr   *v1alpha1.Kubeconfig
		want string
	}{
		"Default": {
			cr: &v1alpha1.Kubeconfig{Spec: v1alpha1.KubeconfigSpec{ForProvider: v1alpha1.KubeconfigParameters{
				Node: "10.0.0.1",
			}}},
			want: "10.0.0.1:50000",
		},
		"EmptyEndpoint": {
			cr: &v1alpha1.Kubeconfig{Spec: v1alpha1.KubeconfigSpec{ForProvider: v1alpha1.KubeconfigParameters{
				Node:     "10.0.0.2",
				Endpoint: &emptyEndpoint,
			}}},
			want: "10.0.0.2:50000",
		},
		"CustomEndpoint": {
			cr: &v1alpha1.Kubeconfig{Spec: v1alpha1.KubeconfigSpec{ForProvider: v1alpha1.KubeconfigParameters{
				Node:     "10.0.0.3",
				Endpoint: &customEndpoint,
			}}},
			want: customEndpoint,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := getKubeconfigEndpoint(tc.cr)
			if got != tc.want {
				t.Fatalf("getKubeconfigEndpoint(...): got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildKubeconfigClientConfig(t *testing.T) {
	caCert, clientCert, clientKey := generateTestCertificates(t)

	tests := map[string]struct {
		clientConfig v1alpha1.ClientConfiguration
		wantErr      string
	}{
		"SecureWithPEMValues": {
			clientConfig: v1alpha1.ClientConfiguration{
				CACertificate:     caCert,
				ClientCertificate: clientCert,
				ClientKey:         clientKey,
			},
		},
		"InvalidCAErrors": {
			clientConfig: v1alpha1.ClientConfiguration{
				CACertificate:     "invalid",
				ClientCertificate: clientCert,
				ClientKey:         clientKey,
			},
			wantErr: "failed to parse CA certificate",
		},
		"InvalidClientCertificateErrors": {
			clientConfig: v1alpha1.ClientConfiguration{
				CACertificate:     caCert,
				ClientCertificate: "invalid",
				ClientKey:         clientKey,
			},
			wantErr: "failed to create client certificate",
		},
		"MissingCACertificateErrors": {
			clientConfig: v1alpha1.ClientConfiguration{
				ClientCertificate: clientCert,
				ClientKey:         clientKey,
			},
			wantErr: "clientConfiguration.caCertificate is required",
		},
		"MissingClientCertificateErrors": {
			clientConfig: v1alpha1.ClientConfiguration{
				CACertificate: caCert,
				ClientKey:     clientKey,
			},
			wantErr: "clientConfiguration.clientCertificate is required",
		},
		"MissingClientKeyErrors": {
			clientConfig: v1alpha1.ClientConfiguration{
				CACertificate:     caCert,
				ClientCertificate: clientCert,
			},
			wantErr: "clientConfiguration.clientKey is required",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := buildKubeconfigClientConfig(tc.clientConfig)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatal("buildKubeconfigClientConfig(...): expected error")
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("buildKubeconfigClientConfig(...): got error %q, want to contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildKubeconfigClientConfig(...): unexpected error: %v", err)
			}
			if diff := cmp.Diff("dynamic", got.Context); diff != "" {
				t.Errorf("buildKubeconfigClientConfig(...).Context: -want, +got:\n%s", diff)
			}
			if len(got.Contexts) != 1 {
				t.Fatalf("buildKubeconfigClientConfig(...): got %d contexts, want 1", len(got.Contexts))
			}
			ctx := got.Contexts["dynamic"]
			if ctx == nil {
				t.Fatal("buildKubeconfigClientConfig(...).Contexts[dynamic] = nil")
			}
			assertBase64Value(t, "CA", ctx.CA, tc.clientConfig.CACertificate)
			assertBase64Value(t, "Crt", ctx.Crt, tc.clientConfig.ClientCertificate)
			assertBase64Value(t, "Key", ctx.Key, tc.clientConfig.ClientKey)
			if len(ctx.Endpoints) != 0 {
				t.Fatalf("buildKubeconfigClientConfig(...).Contexts[dynamic].Endpoints = %v, want empty", ctx.Endpoints)
			}
		})
	}
}

func TestParseKubeconfig(t *testing.T) {
	type want struct {
		configuration *v1alpha1.KubernetesClientConfiguration
		errContains   string
	}

	cases := map[string]struct {
		reason         string
		kubeconfigData string
		want           want
	}{
		"Valid": {
			reason:         "Embedded kubeconfig data should be parsed into status fields.",
			kubeconfigData: testKubeconfig(),
			want: want{
				configuration: testClientConfiguration(),
			},
		},
		"MissingCurrentContext": {
			reason: "A kubeconfig with multiple contexts must specify the selected context.",
			kubeconfigData: `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:6443
    certificate-authority-data: Y2EtY2VydA==
  name: default
contexts:
- context:
    cluster: default
    user: default
  name: default
- context:
    cluster: default
    user: default
  name: other
users:
- name: default
  user:
    client-certificate-data: Y2xpZW50LWNlcnQ=
    client-key-data: Y2xpZW50LWtleQ==
`,
			want: want{errContains: "current context is required"},
		},
		"MissingCluster": {
			reason:         "The current context must refer to an existing cluster.",
			kubeconfigData: strings.Replace(testKubeconfig(), "cluster: default", "cluster: missing", 1),
			want:           want{errContains: `cluster "missing" not found`},
		},
		"MissingAuthInfo": {
			reason:         "The current context must refer to an existing auth info.",
			kubeconfigData: strings.Replace(testKubeconfig(), "user: default", "user: missing", 1),
			want:           want{errContains: `auth info "missing" not found`},
		},
		"PathReferences": {
			reason: "Path-based kubeconfig credentials cannot be represented in durable connection details.",
			kubeconfigData: `apiVersion: v1
kind: Config
current-context: default
clusters:
- cluster:
    server: https://127.0.0.1:6443
    certificate-authority: /tmp/ca.crt
  name: default
contexts:
- context:
    cluster: default
    user: default
  name: default
users:
- name: default
  user:
    client-certificate-data: Y2xpZW50LWNlcnQ=
    client-key-data: Y2xpZW50LWtleQ==
`,
			want: want{errContains: "certificate-authority file references are not supported"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := parseKubeconfig(tc.kubeconfigData)
			if tc.want.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want.errContains) {
					t.Fatalf("\n%s\nparseKubeconfig(...): want error containing %q, got %v", tc.reason, tc.want.errContains, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("\n%s\nparseKubeconfig(...): unexpected error: %v", tc.reason, err)
			}
			if diff := cmp.Diff(tc.want.configuration, got); diff != "" {
				t.Errorf("\n%s\nparseKubeconfig(...): -want, +got:\n%s", tc.reason, diff)
			}
		})
	}
}

func errorsNew(message string) error {
	return errors.New(message)
}

func testKubeClient(t *testing.T, objects ...runtime.Object) ctrlclient.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("corev1.AddToScheme(...): %v", err)
	}

	return fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).Build()
}

func assertBase64Value(t *testing.T, field, got, wantDecoded string) {
	t.Helper()

	decoded, err := base64.StdEncoding.DecodeString(got)
	if err != nil {
		t.Fatalf("%s is not base64 encoded: %v", field, err)
	}
	if diff := cmp.Diff(wantDecoded, string(decoded)); diff != "" {
		t.Fatalf("%s decoded value: -want, +got:\n%s", field, diff)
	}
}

func generateTestCertificates(t *testing.T) (string, string, string) {
	t.Helper()

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey(...): %v", err)
	}

	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("x509.CreateCertificate(...): %v", err)
	}

	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey(...): %v", err)
	}
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, caTemplate, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("x509.CreateCertificate(...): %v", err)
	}

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	clientCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER})
	clientKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(clientKey)})

	return string(caPEM), string(clientCertPEM), string(clientKeyPEM)
}

func testClientConfiguration() *v1alpha1.KubernetesClientConfiguration {
	return &v1alpha1.KubernetesClientConfiguration{
		Host:              "https://127.0.0.1:6443",
		CACertificate:     "ca-cert",
		ClientCertificate: "client-cert",
		ClientKey:         "client-key",
	}
}

func testKubeconfig() string {
	return `apiVersion: v1
kind: Config
current-context: default
clusters:
- cluster:
    server: https://127.0.0.1:6443
    certificate-authority-data: Y2EtY2VydA==
  name: default
contexts:
- context:
    cluster: default
    user: default
  name: default
users:
- name: default
  user:
    client-certificate-data: Y2xpZW50LWNlcnQ=
    client-key-data: Y2xpZW50LWtleQ==
`
}

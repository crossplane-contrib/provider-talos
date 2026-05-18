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

package bootstrap

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

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/meta"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	talosclient "github.com/siderolabs/talos/pkg/machinery/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/crossplane-contrib/provider-talos/apis/machine/v1alpha1"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/crossplane/crossplane-runtime/pkg/test"
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

func TestObserveWithSuccessfulExternalCreate(t *testing.T) {
	createdAt := time.Date(2026, 5, 13, 20, 29, 41, 0, time.UTC)
	cr := testBootstrap()
	meta.SetExternalCreateSucceeded(cr, createdAt)

	e := external{isBootstrappedHealthyFn: func(context.Context, *v1alpha1.Bootstrap) bool { return false }}
	got, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("e.Observe(...): unexpected error: %v", err)
	}

	if !got.ResourceExists {
		t.Fatal("e.Observe(...).ResourceExists = false, want true")
	}
	if got.ResourceUpToDate {
		t.Fatal("e.Observe(...).ResourceUpToDate = true, want false")
	}
	if cr.Status.AtProvider.Bootstrapped {
		t.Fatal("cr.Status.AtProvider.Bootstrapped = true, want false")
	}
	if cr.Status.AtProvider.BootstrapTime != nil {
		t.Fatalf("cr.Status.AtProvider.BootstrapTime = %v, want nil", cr.Status.AtProvider.BootstrapTime)
	}
	if got := cr.Status.GetCondition(xpv1.TypeReady).Status; got == corev1.ConditionTrue {
		t.Fatalf("Ready condition status = %s, want not true", got)
	}
}

func TestObserveWithCachedBootstrappedStatusUnhealthy(t *testing.T) {
	cr := testBootstrap()
	cr.Status.AtProvider.Bootstrapped = true
	now := metav1.Now()
	cr.Status.AtProvider.BootstrapTime = &now

	e := external{isBootstrappedHealthyFn: func(context.Context, *v1alpha1.Bootstrap) bool { return false }}
	got, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("e.Observe(...): unexpected error: %v", err)
	}

	if !got.ResourceExists {
		t.Fatal("e.Observe(...).ResourceExists = false, want true")
	}
	if got.ResourceUpToDate {
		t.Fatal("e.Observe(...).ResourceUpToDate = true, want false")
	}
	if got := cr.Status.GetCondition(xpv1.TypeReady).Status; got == corev1.ConditionTrue {
		t.Fatalf("Ready condition status = %s, want not true", got)
	}
}

func TestObserveAlreadyHealthyWithoutStatus(t *testing.T) {
	cr := testBootstrap()
	e := external{isBootstrappedHealthyFn: func(context.Context, *v1alpha1.Bootstrap) bool { return true }}

	got, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("e.Observe(...): unexpected error: %v", err)
	}

	if !got.ResourceExists {
		t.Fatal("e.Observe(...).ResourceExists = false, want true")
	}
	if !got.ResourceUpToDate {
		t.Fatal("e.Observe(...).ResourceUpToDate = false, want true")
	}
	if !cr.Status.AtProvider.Bootstrapped {
		t.Fatal("cr.Status.AtProvider.Bootstrapped = false, want true")
	}
	if cr.Status.AtProvider.BootstrapTime == nil {
		t.Fatal("cr.Status.AtProvider.BootstrapTime = nil, want timestamp")
	}
}

func TestObserveMissingAndUnhealthy(t *testing.T) {
	cr := testBootstrap()
	e := external{isBootstrappedHealthyFn: func(context.Context, *v1alpha1.Bootstrap) bool { return false }}

	got, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("e.Observe(...): unexpected error: %v", err)
	}

	if got.ResourceExists {
		t.Fatal("e.Observe(...).ResourceExists = true, want false")
	}
	if got.ResourceUpToDate {
		t.Fatal("e.Observe(...).ResourceUpToDate = true, want false")
	}
	if cr.Status.AtProvider.Bootstrapped {
		t.Fatal("cr.Status.AtProvider.Bootstrapped = true, want false")
	}
}

func TestCreateAlreadyExistsHealthyIsSuccess(t *testing.T) {
	cr := testBootstrap()
	e := external{
		bootstrapFn: func(context.Context, *v1alpha1.Bootstrap) error {
			return status.Error(codes.AlreadyExists, "etcd data directory is not empty")
		},
		isBootstrappedHealthyFn: func(context.Context, *v1alpha1.Bootstrap) bool { return true },
	}

	_, err := e.Create(context.Background(), cr)
	if err != nil {
		t.Fatalf("e.Create(...): unexpected error: %v", err)
	}
	if !cr.Status.AtProvider.Bootstrapped {
		t.Fatal("cr.Status.AtProvider.Bootstrapped = false, want true")
	}
	if cr.Status.AtProvider.BootstrapTime == nil {
		t.Fatal("cr.Status.AtProvider.BootstrapTime = nil, want timestamp")
	}
	if got := cr.Status.GetCondition(xpv1.TypeReady).Status; got != corev1.ConditionTrue {
		t.Fatalf("Ready condition status = %s, want %s", got, corev1.ConditionTrue)
	}
}

func TestCreateSuccessfulBootstrapMarksReadyOnlyWhenHealthy(t *testing.T) {
	cr := testBootstrap()
	e := external{
		bootstrapFn:             func(context.Context, *v1alpha1.Bootstrap) error { return nil },
		isBootstrappedHealthyFn: func(context.Context, *v1alpha1.Bootstrap) bool { return true },
	}

	_, err := e.Create(context.Background(), cr)
	if err != nil {
		t.Fatalf("e.Create(...): unexpected error: %v", err)
	}
	if !cr.Status.AtProvider.Bootstrapped {
		t.Fatal("cr.Status.AtProvider.Bootstrapped = false, want true")
	}
	if cr.Status.AtProvider.BootstrapTime == nil {
		t.Fatal("cr.Status.AtProvider.BootstrapTime = nil, want timestamp")
	}
	if got := cr.Status.GetCondition(xpv1.TypeReady).Status; got != corev1.ConditionTrue {
		t.Fatalf("Ready condition status = %s, want %s", got, corev1.ConditionTrue)
	}
}

func TestCreateAlreadyExistsUnhealthyReturnsError(t *testing.T) {
	cr := testBootstrap()
	e := external{
		bootstrapFn: func(context.Context, *v1alpha1.Bootstrap) error {
			return status.Error(codes.AlreadyExists, "etcd data directory is not empty")
		},
		isBootstrappedHealthyFn: func(context.Context, *v1alpha1.Bootstrap) bool { return false },
	}

	_, err := e.Create(context.Background(), cr)
	if err == nil {
		t.Fatal("e.Create(...): expected error")
	}
	if !strings.Contains(err.Error(), "Talos-side bootstrap completion could not be verified") {
		t.Fatalf("e.Create(...): got error %q, want health verification context", err.Error())
	}
}

func TestIsBootstrappedHealthy(t *testing.T) {
	tests := map[string]struct {
		services []talosclient.ServiceInfo
		err      error
		want     bool
	}{
		"RunningHealthyEtcd": {
			services: []talosclient.ServiceInfo{{Service: serviceInfo("etcd", "Running", &machineapi.ServiceHealth{Healthy: true})}},
			want:     true,
		},
		"WaitingForFirstNodeBootstrap": {
			services: []talosclient.ServiceInfo{{Service: serviceInfo("etcd", "Preparing", &machineapi.ServiceHealth{Unknown: true, LastMessage: "waiting: please run talosctl bootstrap"})}},
			want:     false,
		},
		"RunningWithBootstrapRequiredEvent": {
			services: []talosclient.ServiceInfo{{Service: serviceInfo("etcd", "Running", &machineapi.ServiceHealth{Healthy: true}, "please run talosctl bootstrap")}},
			want:     false,
		},
		"NilHealth": {
			services: []talosclient.ServiceInfo{{Service: serviceInfo("etcd", "Running", nil)}},
			want:     false,
		},
		"ServiceInfoError": {
			err:  errors.New("unavailable"),
			want: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := testBootstrap()
			e := external{newBootstrapHealthClientFn: func(context.Context, *v1alpha1.Bootstrap) (bootstrapHealthClient, error) {
				return &fakeBootstrapHealthClient{services: tc.services, err: tc.err}, nil
			}}

			got := e.isBootstrappedHealthy(context.Background(), cr)
			if got != tc.want {
				t.Fatalf("e.isBootstrappedHealthy(...): got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCreateNonAlreadyExistsReturnsError(t *testing.T) {
	cr := testBootstrap()
	e := external{
		bootstrapFn:             func(context.Context, *v1alpha1.Bootstrap) error { return errors.New("permission denied") },
		isBootstrappedHealthyFn: func(context.Context, *v1alpha1.Bootstrap) bool { return true },
	}

	_, err := e.Create(context.Background(), cr)
	if err == nil {
		t.Fatal("e.Create(...): expected error")
	}
	if !strings.Contains(err.Error(), "failed to bootstrap Talos cluster") {
		t.Fatalf("e.Create(...): got error %q, want bootstrap failure context", err.Error())
	}
}

func TestIsBootstrapAlreadyExists(t *testing.T) {
	tests := map[string]struct {
		err  error
		want bool
	}{
		"Nil": {
			want: false,
		},
		"GRPCAlreadyExists": {
			err:  status.Error(codes.AlreadyExists, "etcd data directory is not empty"),
			want: true,
		},
		"WrappedGRPCAlreadyExists": {
			err:  errors.Wrap(status.Error(codes.AlreadyExists, "etcd data directory is not empty"), "failed to bootstrap Talos cluster"),
			want: true,
		},
		"KnownMessage": {
			err:  errors.New("rpc error: code = AlreadyExists desc = etcd data directory is not empty"),
			want: true,
		},
		"Unrelated": {
			err:  errors.New("connection refused"),
			want: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := isBootstrapAlreadyExists(tc.err)
			if got != tc.want {
				t.Fatalf("isBootstrapAlreadyExists(...): got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildBootstrapClientConfig(t *testing.T) {
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
			got, err := buildBootstrapClientConfig(tc.clientConfig)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatal("buildBootstrapClientConfig(...): expected error")
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("buildBootstrapClientConfig(...): got error %q, want to contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildBootstrapClientConfig(...): unexpected error: %v", err)
			}
			if diff := cmp.Diff("dynamic", got.Context); diff != "" {
				t.Errorf("buildBootstrapClientConfig(...).Context: -want, +got:\n%s", diff)
			}
			if len(got.Contexts) != 1 {
				t.Fatalf("buildBootstrapClientConfig(...): got %d contexts, want 1", len(got.Contexts))
			}
			ctx := got.Contexts["dynamic"]
			if ctx == nil {
				t.Fatal("buildBootstrapClientConfig(...).Contexts[dynamic] = nil")
			}
			assertBase64Value(t, "CA", ctx.CA, tc.clientConfig.CACertificate)
			assertBase64Value(t, "Crt", ctx.Crt, tc.clientConfig.ClientCertificate)
			assertBase64Value(t, "Key", ctx.Key, tc.clientConfig.ClientKey)
			if len(ctx.Endpoints) != 0 {
				t.Fatalf("buildBootstrapClientConfig(...).Contexts[dynamic].Endpoints = %v, want empty", ctx.Endpoints)
			}
		})
	}
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

func testBootstrap() *v1alpha1.Bootstrap {
	return &v1alpha1.Bootstrap{
		ObjectMeta: metav1.ObjectMeta{Name: "test-bootstrap"},
		Spec: v1alpha1.BootstrapSpec{ForProvider: v1alpha1.BootstrapParameters{
			Node: "127.0.0.1",
			ClientConfiguration: v1alpha1.ClientConfiguration{
				ClientCertificate: "insecure",
			},
		}},
	}
}

type fakeBootstrapHealthClient struct {
	services []talosclient.ServiceInfo
	err      error
}

func (f *fakeBootstrapHealthClient) ServiceInfo(context.Context, string, ...grpc.CallOption) ([]talosclient.ServiceInfo, error) {
	return f.services, f.err
}

func (f *fakeBootstrapHealthClient) Close() error { return nil }

func serviceInfo(id, state string, health *machineapi.ServiceHealth, eventMessages ...string) *machineapi.ServiceInfo {
	service := &machineapi.ServiceInfo{
		Id:     id,
		State:  state,
		Health: health,
	}
	if len(eventMessages) > 0 {
		service.Events = &machineapi.ServiceEvents{}
		for _, message := range eventMessages {
			service.Events.Events = append(service.Events.Events, &machineapi.ServiceEvent{Msg: message})
		}
	}

	return service
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

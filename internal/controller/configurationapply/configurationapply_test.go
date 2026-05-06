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

package configurationapply

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/siderolabs/talos/pkg/machinery/api/machine"

	v1alpha1 "github.com/crossplane-contrib/provider-talos/apis/machine/v1alpha1"
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

func TestGetConfigurationApplyMode(t *testing.T) {
	empty := ""
	reboot := "reboot"
	auto := "auto"
	noReboot := "no_reboot"
	staged := "staged"
	unknown := "unknown"

	tests := map[string]struct {
		applyMode *string
		want      machine.ApplyConfigurationRequest_Mode
		wantErr   bool
	}{
		"NilDefaultsToReboot": {
			want: machine.ApplyConfigurationRequest_REBOOT,
		},
		"EmptyDefaultsToReboot": {
			applyMode: &empty,
			want:      machine.ApplyConfigurationRequest_REBOOT,
		},
		"Reboot": {
			applyMode: &reboot,
			want:      machine.ApplyConfigurationRequest_REBOOT,
		},
		"Auto": {
			applyMode: &auto,
			want:      machine.ApplyConfigurationRequest_AUTO,
		},
		"NoReboot": {
			applyMode: &noReboot,
			want:      machine.ApplyConfigurationRequest_NO_REBOOT,
		},
		"Staged": {
			applyMode: &staged,
			want:      machine.ApplyConfigurationRequest_STAGED,
		},
		"UnknownErrors": {
			applyMode: &unknown,
			want:      machine.ApplyConfigurationRequest_REBOOT,
			wantErr:   true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := getConfigurationApplyMode(tc.applyMode)
			if tc.wantErr && err == nil {
				t.Fatal("getConfigurationApplyMode(...): expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("getConfigurationApplyMode(...): unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("getConfigurationApplyMode(...): -want, +got:\n%s", diff)
			}
		})
	}
}

func TestBuildConfigurationApplyTLSConfig(t *testing.T) {
	caCert, clientCert, clientKey := generateTestCertificates(t)

	tests := map[string]struct {
		clientConfig v1alpha1.ClientConfiguration
		node         string
		check        func(t *testing.T, cfg *tls.Config)
		wantErr      bool
	}{
		"InsecureEmptyClientCertificate": {
			clientConfig: v1alpha1.ClientConfiguration{},
			check: func(t *testing.T, cfg *tls.Config) {
				t.Helper()
				if !cfg.InsecureSkipVerify {
					t.Fatal("buildConfigurationApplyTLSConfig(...): InsecureSkipVerify = false")
				}
			},
		},
		"InsecureClientCertificateValue": {
			clientConfig: v1alpha1.ClientConfiguration{ClientCertificate: "insecure"},
			check: func(t *testing.T, cfg *tls.Config) {
				t.Helper()
				if !cfg.InsecureSkipVerify {
					t.Fatal("buildConfigurationApplyTLSConfig(...): InsecureSkipVerify = false")
				}
			},
		},
		"SecureWithCA": {
			clientConfig: v1alpha1.ClientConfiguration{
				CACertificate:     caCert,
				ClientCertificate: clientCert,
				ClientKey:         clientKey,
			},
			node: "127.0.0.1",
			check: func(t *testing.T, cfg *tls.Config) {
				t.Helper()
				if len(cfg.Certificates) != 1 {
					t.Fatalf("buildConfigurationApplyTLSConfig(...): got %d certificates, want 1", len(cfg.Certificates))
				}
				if cfg.RootCAs == nil {
					t.Fatal("buildConfigurationApplyTLSConfig(...): RootCAs = nil")
				}
				if diff := cmp.Diff("127.0.0.1", cfg.ServerName); diff != "" {
					t.Errorf("buildConfigurationApplyTLSConfig(...).ServerName: -want, +got:\n%s", diff)
				}
				if diff := cmp.Diff(uint16(tls.VersionTLS12), cfg.MinVersion); diff != "" {
					t.Errorf("buildConfigurationApplyTLSConfig(...).MinVersion: -want, +got:\n%s", diff)
				}
			},
		},
		"InvalidCAErrors": {
			clientConfig: v1alpha1.ClientConfiguration{
				CACertificate:     "invalid",
				ClientCertificate: clientCert,
				ClientKey:         clientKey,
			},
			wantErr: true,
		},
		"InvalidClientCertificateErrors": {
			clientConfig: v1alpha1.ClientConfiguration{
				CACertificate:     caCert,
				ClientCertificate: "invalid",
				ClientKey:         clientKey,
			},
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := buildConfigurationApplyTLSConfig(tc.clientConfig, tc.node)
			if tc.wantErr && err == nil {
				t.Fatal("buildConfigurationApplyTLSConfig(...): expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("buildConfigurationApplyTLSConfig(...): unexpected error: %v", err)
			}
			if tc.check != nil {
				tc.check(t, got)
			}
		})
	}
}

func TestBuildApplyTLSConfig(t *testing.T) {
	caCert, clientCert, clientKey := generateTestCertificates(t)

	tests := map[string]struct {
		maintenanceMode bool
		clientConfig    v1alpha1.ClientConfiguration
		check           func(t *testing.T, cfg *tls.Config, maintenanceMode bool)
		wantErr         bool
	}{
		"MaintenanceModeUsesInsecureTLSWithInlineClientConfiguration": {
			maintenanceMode: true,
			clientConfig: v1alpha1.ClientConfiguration{
				CACertificate:     caCert,
				ClientCertificate: clientCert,
				ClientKey:         clientKey,
			},
			check: func(t *testing.T, cfg *tls.Config, maintenanceMode bool) {
				t.Helper()
				if !maintenanceMode {
					t.Fatal("buildApplyTLSConfig(...): maintenanceMode = false")
				}
				if !cfg.InsecureSkipVerify {
					t.Fatal("buildApplyTLSConfig(...): InsecureSkipVerify = false")
				}
				if len(cfg.Certificates) != 0 {
					t.Fatalf("buildApplyTLSConfig(...): got %d certificates, want 0", len(cfg.Certificates))
				}
			},
		},
		"SecureFallbackUsesInlineClientConfiguration": {
			clientConfig: v1alpha1.ClientConfiguration{
				CACertificate:     caCert,
				ClientCertificate: clientCert,
				ClientKey:         clientKey,
			},
			check: func(t *testing.T, cfg *tls.Config, maintenanceMode bool) {
				t.Helper()
				if maintenanceMode {
					t.Fatal("buildApplyTLSConfig(...): maintenanceMode = true")
				}
				if cfg.InsecureSkipVerify {
					t.Fatal("buildApplyTLSConfig(...): InsecureSkipVerify = true")
				}
				if len(cfg.Certificates) != 1 {
					t.Fatalf("buildApplyTLSConfig(...): got %d certificates, want 1", len(cfg.Certificates))
				}
				if cfg.RootCAs == nil {
					t.Fatal("buildApplyTLSConfig(...): RootCAs = nil")
				}
				if diff := cmp.Diff("127.0.0.1", cfg.ServerName); diff != "" {
					t.Errorf("buildApplyTLSConfig(...).ServerName: -want, +got:\n%s", diff)
				}
			},
		},
		"ExplicitInsecureFallbackPreservesExistingBehavior": {
			clientConfig: v1alpha1.ClientConfiguration{ClientCertificate: "insecure"},
			check: func(t *testing.T, cfg *tls.Config, maintenanceMode bool) {
				t.Helper()
				if maintenanceMode {
					t.Fatal("buildApplyTLSConfig(...): maintenanceMode = true")
				}
				if !cfg.InsecureSkipVerify {
					t.Fatal("buildApplyTLSConfig(...): InsecureSkipVerify = false")
				}
			},
		},
		"InvalidSecureClientConfigurationErrorsAfterMaintenanceProbeFails": {
			clientConfig: v1alpha1.ClientConfiguration{
				CACertificate:     "invalid",
				ClientCertificate: clientCert,
				ClientKey:         clientKey,
			},
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			e := external{
				canConnectInsecureFn: func(context.Context, *v1alpha1.ConfigurationApply) bool {
					return tc.maintenanceMode
				},
			}
			cr := &v1alpha1.ConfigurationApply{
				Spec: v1alpha1.ConfigurationApplySpec{
					ForProvider: v1alpha1.ConfigurationApplyParameters{
						Node:                "127.0.0.1",
						ClientConfiguration: tc.clientConfig,
					},
				},
			}

			got, maintenanceMode, err := e.buildApplyTLSConfig(context.Background(), cr)
			if tc.wantErr && err == nil {
				t.Fatal("buildApplyTLSConfig(...): expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("buildApplyTLSConfig(...): unexpected error: %v", err)
			}
			if tc.check != nil {
				tc.check(t, got, maintenanceMode)
			}
		})
	}
}

func TestGetConfigurationApplyEndpoint(t *testing.T) {
	node := "127.0.0.2"
	customEndpoint := "127.0.0.1:50000"
	emptyEndpoint := ""

	tests := map[string]struct {
		cr   *v1alpha1.ConfigurationApply
		want string
	}{
		"DefaultsToNodePort": {
			cr: &v1alpha1.ConfigurationApply{
				Spec: v1alpha1.ConfigurationApplySpec{
					ForProvider: v1alpha1.ConfigurationApplyParameters{Node: node},
				},
			},
			want: node + ":50000",
		},
		"UsesProvidedEndpoint": {
			cr: &v1alpha1.ConfigurationApply{
				Spec: v1alpha1.ConfigurationApplySpec{
					ForProvider: v1alpha1.ConfigurationApplyParameters{
						Node:     node,
						Endpoint: &customEndpoint,
					},
				},
			},
			want: customEndpoint,
		},
		"IgnoresEmptyEndpoint": {
			cr: &v1alpha1.ConfigurationApply{
				Spec: v1alpha1.ConfigurationApplySpec{
					ForProvider: v1alpha1.ConfigurationApplyParameters{
						Node:     node,
						Endpoint: &emptyEndpoint,
					},
				},
			},
			want: node + ":50000",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := getConfigurationApplyEndpoint(tc.cr)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("getConfigurationApplyEndpoint(...): -want, +got:\n%s", diff)
			}
		})
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

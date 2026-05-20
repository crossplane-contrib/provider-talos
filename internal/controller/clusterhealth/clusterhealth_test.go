/*
Copyright 2025 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package clusterhealth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"

	"github.com/crossplane-contrib/provider-talos/apis/cluster/v1alpha1"
)

func TestObserve(t *testing.T) {
	errBoom := errors.New("invalid config")

	cases := map[string]struct {
		mg       resource.Managed
		check    func(context.Context, *v1alpha1.ClusterHealth) (bool, string, error)
		wantErr  bool
		upToDate bool
		ready    corev1.ConditionStatus
		healthy  bool
		msg      string
	}{
		"WrongType": {mg: &v1alpha1.Kubeconfig{}, wantErr: true},
		"Healthy": {
			mg: testClusterHealth(3, 2), check: func(context.Context, *v1alpha1.ClusterHealth) (bool, string, error) {
				return true, "cluster is healthy", nil
			},
			upToDate: true, ready: corev1.ConditionTrue, healthy: true, msg: "cluster is healthy",
		},
		"Unhealthy": {
			mg: testClusterHealth(1, 0), check: func(context.Context, *v1alpha1.ClusterHealth) (bool, string, error) {
				return false, "waiting for kubelet", nil
			},
			upToDate: false, ready: corev1.ConditionFalse, healthy: false, msg: "waiting for kubelet",
		},
		"InvalidConfig": {
			mg: testClusterHealth(1, 0), check: func(context.Context, *v1alpha1.ClusterHealth) (bool, string, error) { return false, "", errBoom },
			wantErr: true, upToDate: false, ready: corev1.ConditionFalse, healthy: false, msg: errBoom.Error(),
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := external{checkClusterHealthFn: tc.check}
			got, err := e.Observe(context.Background(), tc.mg)
			if tc.wantErr && err == nil {
				t.Fatal("Observe() error = nil, want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Observe() unexpected error: %v", err)
			}
			cr, ok := tc.mg.(*v1alpha1.ClusterHealth)
			if !ok {
				return
			}
			if !got.ResourceExists {
				t.Fatal("ResourceExists = false, want true")
			}
			if got.ResourceUpToDate != tc.upToDate {
				t.Fatalf("ResourceUpToDate = %v, want %v", got.ResourceUpToDate, tc.upToDate)
			}
			if cr.Status.GetCondition(xpv1.TypeReady).Status != tc.ready {
				t.Fatalf("Ready = %s, want %s", cr.Status.GetCondition(xpv1.TypeReady).Status, tc.ready)
			}
			if cr.Status.AtProvider.Healthy != tc.healthy {
				t.Fatalf("Healthy = %v, want %v", cr.Status.AtProvider.Healthy, tc.healthy)
			}
			if cr.Status.AtProvider.LastMessage != tc.msg {
				t.Fatalf("LastMessage = %q, want %q", cr.Status.AtProvider.LastMessage, tc.msg)
			}
			if cr.Status.AtProvider.LastCheckTime == nil {
				t.Fatal("LastCheckTime = nil, want set")
			}
			if tc.healthy && cr.Status.AtProvider.LastHealthyTime == nil {
				t.Fatal("LastHealthyTime = nil, want set")
			}
			if cr.Status.AtProvider.CheckedControlPlaneNodes != len(cr.Spec.ForProvider.ControlPlaneNodes) {
				t.Fatal("CheckedControlPlaneNodes not set")
			}
			if cr.Status.AtProvider.CheckedWorkerNodes != len(cr.Spec.ForProvider.WorkerNodes) {
				t.Fatal("CheckedWorkerNodes not set")
			}
		})
	}
}

func TestNoOpLifecycle(t *testing.T) {
	e := external{}
	cr := testClusterHealth(1, 0)
	if _, err := e.Create(context.Background(), cr); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if _, err := e.Update(context.Background(), cr); err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
	if _, err := e.Delete(context.Background(), cr); err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
}

func TestValidateClusterHealthSpec(t *testing.T) {
	cr := testClusterHealth(1, 0)
	if err := validateClusterHealthSpec(cr); err != nil {
		t.Fatalf("validateClusterHealthSpec() unexpected error: %v", err)
	}
	cr.Spec.ForProvider.Endpoints = nil
	if err := validateClusterHealthSpec(cr); err == nil {
		t.Fatal("validateClusterHealthSpec() error = nil, want error")
	}
	cr = testClusterHealth(0, 0)
	if err := validateClusterHealthSpec(cr); err == nil {
		t.Fatal("validateClusterHealthSpec() error = nil, want error")
	}
}

func TestBuildClusterHealthClientConfig(t *testing.T) {
	cfg := validClientConfiguration(t)
	if _, err := buildClusterHealthClientConfig(cfg); err != nil {
		t.Fatalf("buildClusterHealthClientConfig() unexpected error: %v", err)
	}
	cfg.ClientCertificate = ""
	if _, err := buildClusterHealthClientConfig(cfg); err == nil {
		t.Fatal("missing client cert error = nil, want error")
	}
	cfg = validClientConfiguration(t)
	cfg.ClientKey = "not pem"
	if _, err := buildClusterHealthClientConfig(cfg); err == nil {
		t.Fatal("invalid PEM error = nil, want error")
	}
}

func TestSkipKubernetesChecksPassedToChecker(t *testing.T) {
	cr := testClusterHealth(1, 0)
	skip := true
	cr.Spec.ForProvider.SkipKubernetesChecks = &skip
	called := false
	e := external{checkClusterHealthFn: func(_ context.Context, got *v1alpha1.ClusterHealth) (bool, string, error) {
		called = true
		if got.Spec.ForProvider.SkipKubernetesChecks == nil || !*got.Spec.ForProvider.SkipKubernetesChecks {
			t.Fatal("SkipKubernetesChecks was not passed to checker")
		}
		return false, "waiting", nil
	}}
	_, _ = e.Observe(context.Background(), cr)
	if !called {
		t.Fatal("checker was not called")
	}
}

func testClusterHealth(cp, workers int) *v1alpha1.ClusterHealth {
	cps := make([]string, cp)
	for i := range cps {
		cps[i] = "10.0.0." + string(rune('1'+i))
	}
	ws := make([]string, workers)
	for i := range ws {
		ws[i] = "10.0.1." + string(rune('1'+i))
	}
	return &v1alpha1.ClusterHealth{Spec: v1alpha1.ClusterHealthSpec{ForProvider: v1alpha1.ClusterHealthParameters{Endpoints: []string{"10.0.0.1:50000"}, ControlPlaneNodes: cps, WorkerNodes: ws, ClientConfiguration: validClientConfigurationMust()}}}
}

func validClientConfiguration(t *testing.T) v1alpha1.ClientConfiguration {
	t.Helper()
	cert, key, err := testCertAndKey()
	if err != nil {
		t.Fatal(err)
	}
	return v1alpha1.ClientConfiguration{CACertificate: cert, ClientCertificate: cert, ClientKey: key}
}

func validClientConfigurationMust() v1alpha1.ClientConfiguration {
	cert, key, err := testCertAndKey()
	if err != nil {
		panic(err)
	}
	return v1alpha1.ClientConfiguration{CACertificate: cert, ClientCertificate: cert, ClientKey: key}
}

func testCertAndKey() (string, string, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return string(certPEM), string(keyPEM), nil
}

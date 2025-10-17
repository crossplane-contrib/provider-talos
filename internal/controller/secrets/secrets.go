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
	"context"
	"encoding/json"
	"fmt"

	"github.com/crossplane/crossplane-runtime/pkg/feature"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/connection"
	"github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/crossplane/crossplane-runtime/pkg/statemetrics"

	"github.com/crossplane-contrib/provider-talos/apis/machine/v1alpha1"
	apisv1alpha1 "github.com/crossplane-contrib/provider-talos/apis/v1alpha1"
	"github.com/crossplane-contrib/provider-talos/internal/features"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

const (
	errNotSecrets   = "managed resource is not a Secrets custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage"
	errGetPC        = "cannot get ProviderConfig"
	errGetCreds     = "cannot get credentials"

	errNewClient = "cannot create new Service"
)

// TalosSecretsService manages Talos machine secrets
type TalosSecretsService struct {
	credentials []byte
}

// GenerateSecrets generates new Talos machine secrets
type GeneratedSecrets struct {
	Cluster     *secrets.Cluster
	Secrets     *secrets.Bundle
	TalosConfig []byte
}

// NewTalosSecretsService creates a new secrets service with credentials
func NewTalosSecretsService(credentials []byte) (interface{}, error) {
	// Store credentials for client creation - they contain TLS certificates for Talos API
	return &TalosSecretsService{
		credentials: credentials,
	}, nil
}

var (
	newTalosSecretsService = NewTalosSecretsService
)

// Setup adds a controller that reconciles Secrets managed resources.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(v1alpha1.SecretsGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), apisv1alpha1.StoreConfigGroupVersionKind))
	}

	opts := []managed.ReconcilerOption{
		managed.WithExternalConnecter(&connector{
			kube:         mgr.GetClient(),
			usage:        resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			newServiceFn: newTalosSecretsService}),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithPollInterval(o.PollInterval),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithConnectionPublishers(cps...),
		managed.WithManagementPolicies(),
	}

	if o.Features.Enabled(feature.EnableAlphaChangeLogs) {
		opts = append(opts, managed.WithChangeLogger(o.ChangeLogOptions.ChangeLogger))
	}

	if o.MetricOptions != nil {
		opts = append(opts, managed.WithMetricRecorder(o.MetricOptions.MRMetrics))
	}

	if o.MetricOptions != nil && o.MetricOptions.MRStateMetrics != nil {
		stateMetricsRecorder := statemetrics.NewMRStateRecorder(
			mgr.GetClient(), o.Logger, o.MetricOptions.MRStateMetrics, &v1alpha1.SecretsList{}, o.MetricOptions.PollStateMetricInterval,
		)
		if err := mgr.Add(stateMetricsRecorder); err != nil {
			return errors.Wrap(err, "cannot register MR state metrics recorder for kind v1alpha1.SecretsList")
		}
	}

	r := managed.NewReconciler(mgr, resource.ManagedKind(v1alpha1.SecretsGroupVersionKind), opts...)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o.ForControllerRuntime()).
		WithEventFilter(resource.DesiredStateChanged()).
		For(&v1alpha1.Secrets{}).
		Complete(ratelimiter.NewReconciler(name, r, o.GlobalRateLimiter))
}

// A connector is expected to produce an ExternalClient when its Connect method
// is called.
type connector struct {
	kube         client.Client
	usage        resource.Tracker
	newServiceFn func(creds []byte) (interface{}, error)
}

// Connect typically produces an ExternalClient by:
// 1. Tracking that the managed resource is using a ProviderConfig.
// 2. Getting the managed resource's ProviderConfig.
// 3. Getting the credentials specified by the ProviderConfig.
// 4. Using the credentials to form a client.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.Secrets)
	if !ok {
		return nil, errors.New(errNotSecrets)
	}

	if err := c.usage.Track(ctx, mg); err != nil {
		return nil, errors.Wrap(err, errTrackPCUsage)
	}

	pc := &apisv1alpha1.ProviderConfig{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: cr.GetProviderConfigReference().Name}, pc); err != nil {
		return nil, errors.Wrap(err, errGetPC)
	}

	cd := pc.Spec.Credentials
	data, err := resource.CommonCredentialExtractor(ctx, cd.Source, c.kube, cd.CommonCredentialSelectors)
	if err != nil {
		return nil, errors.Wrap(err, errGetCreds)
	}

	svc, err := c.newServiceFn(data)
	if err != nil {
		return nil, errors.Wrap(err, errNewClient)
	}

	return &external{service: svc.(*TalosSecretsService)}, nil
}

// An ExternalClient observes, then either creates, updates, or deletes an
// external resource to ensure it reflects the managed resource's desired state.
type external struct {
	service *TalosSecretsService
}

// TalosCredentials represents the expected structure of Talos provider credentials
type TalosCredentials struct {
	CACertificate     string `json:"ca_certificate,omitempty"`
	ClientCertificate string `json:"client_certificate,omitempty"`
	ClientKey         string `json:"client_key,omitempty"`
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.Secrets)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotSecrets)
	}

	// Debug logging
	fmt.Printf("Observing Secrets: %s\n", cr.Name)
	fmt.Printf("  MachineSecrets nil: %v\n", cr.Status.AtProvider.MachineSecrets == nil)
	fmt.Printf("  ClientConfiguration nil: %v\n", cr.Status.AtProvider.ClientConfiguration == nil)

	// Check if secrets already exist in status (locally generated)
	statusExists := cr.Status.AtProvider.MachineSecrets != nil && cr.Status.AtProvider.ClientConfiguration != nil

	// Secrets are local resources - they're always generated locally
	resourceExists := statusExists
	resourceUpToDate := statusExists

	connectionDetails := managed.ConnectionDetails{}
	if resourceExists && cr.Status.AtProvider.ClientConfiguration != nil {
		// Store client configuration as connection details
		connectionDetails["ca_certificate"] = []byte(cr.Status.AtProvider.ClientConfiguration.CACertificate)
		connectionDetails["client_certificate"] = []byte(cr.Status.AtProvider.ClientConfiguration.ClientCertificate)
		connectionDetails["client_key"] = []byte(cr.Status.AtProvider.ClientConfiguration.ClientKey)
	}

	// Set conditions based on actual state
	if resourceExists && resourceUpToDate {
		cr.SetConditions(xpv1.Available())
	} else {
		cr.SetConditions(xpv1.Unavailable())
	}

	return managed.ExternalObservation{
		ResourceExists:    resourceExists,
		ResourceUpToDate:  resourceUpToDate,
		ConnectionDetails: connectionDetails,
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Secrets)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotSecrets)
	}

	fmt.Printf("Creating Secrets: %s\n", cr.Name)

	// Generate new machine secrets using Talos SDK
	generatedSecrets, err := c.generateMachineSecrets(cr.Spec.ForProvider.TalosVersion)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to generate machine secrets")
	}

	// Update the resource status with generated secrets
	cr.Status.AtProvider.MachineSecrets = &v1alpha1.MachineSecretsData{
		ClusterSecrets:    generatedSecrets.ClusterSecrets,
		KubernetesSecrets: generatedSecrets.KubernetesSecrets,
		TrustdInfo:        generatedSecrets.TrustdInfo,
	}
	cr.Status.AtProvider.ClientConfiguration = &v1alpha1.ClientConfiguration{
		CACertificate:     generatedSecrets.CACertificate,
		ClientCertificate: generatedSecrets.ClientCertificate,
		ClientKey:         generatedSecrets.ClientKey,
	}

	// Return connection details for the secret
	connectionDetails := managed.ConnectionDetails{
		"ca_certificate":     []byte(generatedSecrets.CACertificate),
		"client_certificate": []byte(generatedSecrets.ClientCertificate),
		"client_key":         []byte(generatedSecrets.ClientKey),
		"talos_config":       generatedSecrets.TalosConfig,
	}

	return managed.ExternalCreation{
		ConnectionDetails: connectionDetails,
	}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	_, ok := mg.(*v1alpha1.Secrets)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotSecrets)
	}

	// MachineSecrets are immutable - no updates allowed
	// This should not be called since ResourceUpToDate is always true in Observe
	return managed.ExternalUpdate{}, errors.New("machine secrets are immutable and cannot be updated")
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	_, ok := mg.(*v1alpha1.Secrets)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotSecrets)
	}

	// For MachineSecrets, deletion just clears the generated secrets from status
	// No external cleanup needed since these are just generated values
	// The status will be cleared by the managed resource reconciler

	return managed.ExternalDelete{}, nil
}

func (c *external) Disconnect(ctx context.Context) error {
	// No persistent client to close - clients are created per-request
	return nil
}

// GeneratedSecretsResult contains the generated Talos secrets
type GeneratedSecretsResult struct {
	ClusterSecrets    string
	KubernetesSecrets string
	TrustdInfo        string
	CACertificate     string
	ClientCertificate string
	ClientKey         string
	TalosConfig       []byte
}

// generateMachineSecrets generates new Talos machine secrets using the Talos SDK
func (c *external) generateMachineSecrets(talosVersion *string) (*GeneratedSecretsResult, error) {
	// TODO: Use talosVersion parameter to generate version-specific secrets
	_ = talosVersion // suppress unused parameter warning until implementation

	// Generate machine secrets bundle using current time
	clock := secrets.NewClock()
	secretsBundle, err := secrets.NewBundle(clock, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate secrets bundle")
	}

	// Extract cluster secrets as JSON
	clusterSecretsData := map[string]interface{}{
		"id":     secretsBundle.Cluster.ID,
		"secret": secretsBundle.Cluster.Secret,
	}
	clusterSecretsJSON, err := json.Marshal(clusterSecretsData)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal cluster secrets")
	}

	// Extract Kubernetes secrets as JSON
	kubernetesSecretsData := map[string]interface{}{
		"ca": map[string]interface{}{
			"crt": string(secretsBundle.Certs.K8s.Crt),
			"key": string(secretsBundle.Certs.K8s.Key),
		},
		"aggregatorCA": map[string]interface{}{
			"crt": string(secretsBundle.Certs.K8sAggregator.Crt),
			"key": string(secretsBundle.Certs.K8sAggregator.Key),
		},
	}
	kubernetesSecretsJSON, err := json.Marshal(kubernetesSecretsData)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal kubernetes secrets")
	}

	// Extract TrustD info as JSON
	trustdInfoData := map[string]interface{}{
		"token": secretsBundle.TrustdInfo.Token,
	}
	trustdInfoJSON, err := json.Marshal(trustdInfoData)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal trustd info")
	}

	// Create a basic talos config structure
	talosConfig := map[string]interface{}{
		"context": "default",
		"contexts": map[string]interface{}{
			"default": map[string]interface{}{
				"ca":  string(secretsBundle.Certs.OS.Crt),
				"crt": string(secretsBundle.Certs.OS.Crt),
				"key": string(secretsBundle.Certs.OS.Key),
			},
		},
	}
	talosConfigBytes, err := json.Marshal(talosConfig)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal talos config")
	}

	return &GeneratedSecretsResult{
		ClusterSecrets:    string(clusterSecretsJSON),
		KubernetesSecrets: string(kubernetesSecretsJSON),
		TrustdInfo:        string(trustdInfoJSON),
		CACertificate:     string(secretsBundle.Certs.OS.Crt),
		ClientCertificate: string(secretsBundle.Certs.OS.Crt),
		ClientKey:         string(secretsBundle.Certs.OS.Key),
		TalosConfig:       talosConfigBytes,
	}, nil
}

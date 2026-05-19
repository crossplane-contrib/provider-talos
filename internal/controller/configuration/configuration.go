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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/crossplane/crossplane-runtime/pkg/feature"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	talosconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"
	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	talossecrets "github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	"github.com/siderolabs/talos/pkg/machinery/constants"

	"github.com/crossplane/crossplane-runtime/pkg/connection"
	"github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/crossplane/crossplane-runtime/pkg/statemetrics"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"

	machinev1alpha1 "github.com/crossplane-contrib/provider-talos/apis/machine/v1alpha1"
	apisv1alpha1 "github.com/crossplane-contrib/provider-talos/apis/v1alpha1"
	secretscontroller "github.com/crossplane-contrib/provider-talos/internal/controller/secrets"
	"github.com/crossplane-contrib/provider-talos/internal/features"
)

const (
	errNotConfiguration = "managed resource is not a Configuration custom resource"
	errTrackPCUsage     = "cannot track ProviderConfig usage"
	errGetPC            = "cannot get ProviderConfig"
	errGetCreds         = "cannot get credentials"

	errNewClient = "cannot create new Service"

	connectionKeyMachineConfiguration = "machine_configuration"
	connectionKeyMachineSecrets       = "machine_secrets"
	connectionKeyMachineSecretsBundle = "machine_secrets_bundle"
)

// TalosConfigurationService manages Talos machine configurations
type TalosConfigurationService struct {
	credentials []byte
}

// NewTalosConfigurationService creates a new configuration service with credentials
func NewTalosConfigurationService(credentials []byte) (interface{}, error) {
	// Store credentials for client creation - they contain TLS certificates for Talos API
	return &TalosConfigurationService{
		credentials: credentials,
	}, nil
}

var (
	newTalosConfigurationService = NewTalosConfigurationService
)

// Setup adds a controller that reconciles Configuration managed resources.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(machinev1alpha1.ConfigurationGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), apisv1alpha1.StoreConfigGroupVersionKind))
	}

	opts := []managed.ReconcilerOption{
		managed.WithExternalConnecter(&connector{
			kube:         mgr.GetClient(),
			usage:        resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			newServiceFn: newTalosConfigurationService}),
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
			mgr.GetClient(), o.Logger, o.MetricOptions.MRStateMetrics, &machinev1alpha1.ConfigurationList{}, o.MetricOptions.PollStateMetricInterval,
		)
		if err := mgr.Add(stateMetricsRecorder); err != nil {
			return errors.Wrap(err, "cannot register MR state metrics recorder for kind v1alpha1.ConfigurationList")
		}
	}

	r := managed.NewReconciler(mgr, resource.ManagedKind(machinev1alpha1.ConfigurationGroupVersionKind), opts...)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o.ForControllerRuntime()).
		WithEventFilter(resource.DesiredStateChanged()).
		For(&machinev1alpha1.Configuration{}).
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
	cr, ok := mg.(*machinev1alpha1.Configuration)
	if !ok {
		return nil, errors.New(errNotConfiguration)
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

	return &external{kube: c.kube, service: svc.(*TalosConfigurationService)}, nil
}

// An ExternalClient observes, then either creates, updates, or deletes an
// external resource to ensure it reflects the managed resource's desired state.
type external struct {
	kube    client.Client
	service *TalosConfigurationService
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*machinev1alpha1.Configuration)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotConfiguration)
	}

	// Configuration is a local resource - it generates config locally
	// Always consider it as existing since we can generate it anytime
	machineConfig, err := c.generateMachineConfiguration(ctx, cr)
	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, "failed to generate machine configuration")
	}

	// Update status with generated configuration
	cr.Status.AtProvider.MachineConfiguration = machineConfig
	now := metav1.Now()
	cr.Status.AtProvider.GeneratedTime = &now
	hash := sha256.Sum256([]byte(machineConfig))
	cr.Status.AtProvider.MachineConfigurationHash = hex.EncodeToString(hash[:])

	// Set Ready condition
	cr.SetConditions(xpv1.Available())

	return managed.ExternalObservation{
		ResourceExists:    true,
		ResourceUpToDate:  true,
		ConnectionDetails: managed.ConnectionDetails{connectionKeyMachineConfiguration: []byte(machineConfig)},
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	_, ok := mg.(*machinev1alpha1.Configuration)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotConfiguration)
	}

	// Configuration is generated in Observe - Create is a no-op
	return managed.ExternalCreation{
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	_, ok := mg.(*machinev1alpha1.Configuration)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotConfiguration)
	}

	// Configuration is regenerated in Observe - Update is a no-op
	return managed.ExternalUpdate{
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	_, ok := mg.(*machinev1alpha1.Configuration)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotConfiguration)
	}

	// For Talos configurations, deletion typically means resetting to a default config
	// or removing custom configurations. The exact behavior depends on requirements.
	// For now, we'll log the deletion without taking action on the machine.

	return managed.ExternalDelete{}, nil
}

func (c *external) Disconnect(ctx context.Context) error {
	// No persistent client to close - clients are created per-request
	return nil
}

// generateMachineConfiguration renders Talos machine configuration with the Talos SDK.
func (c *external) generateMachineConfiguration(ctx context.Context, cr *machinev1alpha1.Configuration) (string, error) {
	clusterName, clusterEndpoint, kubernetesVersion := configurationInput(cr)
	options, err := c.generationOptions(ctx, cr)
	if err != nil {
		return "", err
	}

	input, err := generate.NewInput(clusterName, clusterEndpoint, kubernetesVersion, options...)
	if err != nil {
		return "", err
	}

	machineType, err := machine.ParseType(cr.Spec.ForProvider.MachineType)
	if err != nil {
		return "", err
	}

	config, err := input.Config(machineType)
	if err != nil {
		return "", err
	}

	return renderMachineConfig(config, cr.Spec.ForProvider.ConfigPatches)
}

func configurationInput(cr *machinev1alpha1.Configuration) (string, string, string) {
	clusterName := "talos-cluster"
	if cr.Spec.ForProvider.ClusterName != "" {
		clusterName = cr.Spec.ForProvider.ClusterName
	}

	clusterEndpoint := "https://192.168.120.83:6443"
	if cr.Spec.ForProvider.ClusterEndpoint != "" {
		clusterEndpoint = cr.Spec.ForProvider.ClusterEndpoint
	}

	kubernetesVersion := constants.DefaultKubernetesVersion
	if cr.Spec.ForProvider.KubernetesVersion != nil && *cr.Spec.ForProvider.KubernetesVersion != "" {
		kubernetesVersion = *cr.Spec.ForProvider.KubernetesVersion
	}

	return clusterName, clusterEndpoint, kubernetesVersion
}

func (c *external) generationOptions(ctx context.Context, cr *machinev1alpha1.Configuration) ([]generate.Option, error) {
	options := []generate.Option{}
	if cr.Spec.ForProvider.TalosVersion != nil && *cr.Spec.ForProvider.TalosVersion != "" {
		versionContract, err := talosconfig.ParseContractFromVersion(*cr.Spec.ForProvider.TalosVersion)
		if err != nil {
			return nil, err
		}
		options = append(options, generate.WithVersionContract(versionContract))
	}

	secretsBundle, err := c.getMachineSecretsBundle(ctx, cr)
	if err != nil {
		return nil, err
	}
	if secretsBundle != nil {
		options = append(options, generate.WithSecretsBundle(secretsBundle))
	}

	return options, nil
}

func renderMachineConfig(config talosconfig.Provider, configPatches []string) (string, error) {
	if len(configPatches) == 0 {
		configBytes, err := config.Bytes()
		return string(configBytes), err
	}

	patches, err := configpatcher.LoadPatches(configPatches)
	if err != nil {
		return "", err
	}

	patched, err := configpatcher.Apply(configpatcher.WithConfig(config), patches)
	if err != nil {
		return "", err
	}

	configBytes, err := patched.Bytes()
	if err != nil {
		return "", err
	}

	return string(configBytes), nil
}

func (c *external) getMachineSecretsBundle(ctx context.Context, cr *machinev1alpha1.Configuration) (*talossecrets.Bundle, error) {
	if cr.Spec.ForProvider.MachineSecretsRef == nil {
		return nil, errors.New("machineSecretsRef is required to generate deterministic machine configuration")
	}
	if c.kube == nil {
		return nil, errors.New("cannot resolve machineSecretsRef without Kubernetes client")
	}

	secretsResource := &machinev1alpha1.Secrets{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: cr.Spec.ForProvider.MachineSecretsRef.Name}, secretsResource); err != nil {
		return nil, errors.Wrap(err, "cannot get referenced machine secrets")
	}
	if secretsResource.Spec.WriteConnectionSecretToReference == nil {
		return nil, errors.New("referenced machine secrets must define writeConnectionSecretToRef")
	}

	ref := secretsResource.Spec.WriteConnectionSecretToReference
	connectionSecret := &corev1.Secret{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: ref.Namespace}, connectionSecret); err != nil {
		return nil, errors.Wrapf(err, "cannot get referenced machine secrets connection secret %s/%s", ref.Namespace, ref.Name)
	}

	if bundleJSON, ok := connectionSecret.Data[connectionKeyMachineSecretsBundle]; ok {
		bundle := &talossecrets.Bundle{Clock: talossecrets.NewClock()}
		if err := json.Unmarshal(bundleJSON, bundle); err != nil {
			return nil, errors.Wrap(err, "cannot decode referenced machine secrets bundle")
		}
		bundle.Clock = talossecrets.NewClock()

		return bundle, nil
	}

	if structuredJSON, ok := connectionSecret.Data[connectionKeyMachineSecrets]; ok {
		machineSecrets := &machinev1alpha1.MachineSecrets{}
		if err := json.Unmarshal(structuredJSON, machineSecrets); err != nil {
			return nil, errors.Wrap(err, "cannot decode referenced structured machine secrets")
		}

		return secretscontroller.MachineSecretsToSecretsBundle(machineSecrets)
	}

	return nil, errors.Errorf("referenced machine secrets connection secret %s/%s is missing %q or %q", ref.Namespace, ref.Name, connectionKeyMachineSecretsBundle, connectionKeyMachineSecrets)
}

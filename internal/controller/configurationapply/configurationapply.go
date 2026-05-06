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
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	talosclient "github.com/siderolabs/talos/pkg/machinery/client"

	"github.com/crossplane/crossplane-runtime/pkg/feature"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

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
)

const (
	errNotConfigurationApply = "managed resource is not a ConfigurationApply custom resource"
	errTrackPCUsage          = "cannot track ProviderConfig usage"
	errGetPC                 = "cannot get ProviderConfig"
	errGetCreds              = "cannot get credentials"

	errNewClient = "cannot create new Service"
)

// A NoOpService does nothing.
type NoOpService struct{}

var (
	newNoOpService = func(_ []byte) (interface{}, error) { return &NoOpService{}, nil }
)

// Setup adds a controller that reconciles ConfigurationApply managed resources.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(v1alpha1.ConfigurationApplyGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), apisv1alpha1.StoreConfigGroupVersionKind))
	}

	opts := []managed.ReconcilerOption{
		managed.WithExternalConnecter(&connector{
			kube:         mgr.GetClient(),
			usage:        resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			newServiceFn: newNoOpService}),
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
			mgr.GetClient(), o.Logger, o.MetricOptions.MRStateMetrics, &v1alpha1.ConfigurationApplyList{}, o.MetricOptions.PollStateMetricInterval,
		)
		if err := mgr.Add(stateMetricsRecorder); err != nil {
			return errors.Wrap(err, "cannot register MR state metrics recorder for kind v1alpha1.ConfigurationApplyList")
		}
	}

	r := managed.NewReconciler(mgr, resource.ManagedKind(v1alpha1.ConfigurationApplyGroupVersionKind), opts...)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o.ForControllerRuntime()).
		WithEventFilter(resource.DesiredStateChanged()).
		For(&v1alpha1.ConfigurationApply{}).
		Complete(ratelimiter.NewReconciler(name, r, o.GlobalRateLimiter))
}

// A connector is expected to produce an ExternalClient when its Connect method
// is called.
type connector struct {
	kube         ctrlclient.Client
	usage        resource.Tracker
	newServiceFn func(creds []byte) (interface{}, error)
}

// Connect typically produces an ExternalClient by:
// 1. Tracking that the managed resource is using a ProviderConfig.
// 2. Getting the managed resource's ProviderConfig.
// 3. Getting the credentials specified by the ProviderConfig.
// 4. Using the credentials to form a client.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.ConfigurationApply)
	if !ok {
		return nil, errors.New(errNotConfigurationApply)
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

	return &external{
		service:            svc,
		providerConfigData: data,
	}, nil
}

// An ExternalClient observes, then either creates, updates, or deletes an
// external resource to ensure it reflects the managed resource's desired state.
type external struct {
	// A 'client' used to connect to the external resource API. In practice this
	// would be something like an AWS SDK client.
	service interface{}
	// ProviderConfig credentials for verifying machine state
	providerConfigData []byte
	// canConnectInsecureFn allows tests to stub maintenance-mode detection.
	canConnectInsecureFn func(context.Context, *v1alpha1.ConfigurationApply) bool
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.ConfigurationApply)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotConfigurationApply)
	}

	fmt.Printf("Observing ConfigurationApply: %s\n", cr.Name)

	// Check the actual state of the machine
	machineState := c.checkMachineState(ctx, cr)

	// Update status with detected machine state
	now := metav1.Now()
	cr.Status.AtProvider.MachineState = string(machineState)
	cr.Status.AtProvider.LastStateCheck = &now

	// Check if we have a valid machine configuration from structured fields
	hasValidConfig := cr.Spec.ForProvider.MachineConfiguration.Version != "" &&
		cr.Spec.ForProvider.MachineConfiguration.Machine.Type != "" &&
		cr.Spec.ForProvider.MachineConfiguration.Machine.Token != "" &&
		cr.Spec.ForProvider.MachineConfiguration.Cluster.ID != ""

	// Determine resource state based on machine state
	var resourceExists, resourceUpToDate bool

	switch machineState {
	case MachineStateMaintenanceMode:
		// Machine is in maintenance mode - configuration needs to be applied
		resourceExists = false
		resourceUpToDate = false
		cr.Status.AtProvider.Applied = false
		fmt.Printf("Machine %s in maintenance mode - configuration not applied\n", cr.Spec.ForProvider.Node)

	case MachineStateConfigured:
		// Machine is configured and running - verified with authenticated connection
		resourceExists = true
		resourceUpToDate = hasValidConfig
		cr.Status.AtProvider.Applied = true
		cr.SetConditions(xpv1.Available())
		fmt.Printf("Machine %s is configured and running\n", cr.Spec.ForProvider.Node)

	case MachineStateUnreachable:
		// Machine is unreachable - could be installing or failed
		if cr.Status.AtProvider.Applied {
			// We previously applied config - assume it's installing/rebooting (expected)
			resourceExists = true
			resourceUpToDate = hasValidConfig
			cr.SetConditions(xpv1.Available())
			fmt.Printf("Machine %s unreachable but configuration was applied - likely installing\n", cr.Spec.ForProvider.Node)
		} else {
			// Never applied config and unreachable - needs configuration
			resourceExists = false
			resourceUpToDate = false
			fmt.Printf("Machine %s unreachable and configuration not applied\n", cr.Spec.ForProvider.Node)
		}
	}

	return managed.ExternalObservation{
		ResourceExists:    resourceExists,
		ResourceUpToDate:  resourceUpToDate,
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.ConfigurationApply)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotConfigurationApply)
	}

	fmt.Printf("Applying Configuration to Node: %s\n", cr.Spec.ForProvider.Node)

	// Apply configuration to the Talos machine
	err := c.applyConfigurationToNode(ctx, cr)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to apply configuration to node")
	}

	// Update status
	cr.Status.AtProvider.Applied = true
	// Note: LastAppliedTime field doesn't exist in the generated API, skipping

	// Set Ready condition since configuration was successfully applied
	cr.SetConditions(xpv1.Available())

	fmt.Printf("Successfully marked ConfigurationApply as applied: %s\n", cr.Name)

	return managed.ExternalCreation{
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.ConfigurationApply)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotConfigurationApply)
	}

	fmt.Printf("Updating Configuration on Node: %s\n", cr.Spec.ForProvider.Node)

	// Reapply configuration to the Talos machine
	err := c.applyConfigurationToNode(ctx, cr)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to apply configuration to node")
	}

	// Update status
	cr.Status.AtProvider.Applied = true
	// Note: LastAppliedTime field doesn't exist in the generated API, skipping

	return managed.ExternalUpdate{
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.ConfigurationApply)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotConfigurationApply)
	}

	fmt.Printf("Deleting: %+v", cr)

	return managed.ExternalDelete{}, nil
}

func (c *external) Disconnect(ctx context.Context) error {
	return nil
}

// MachineState represents the detected state of the Talos machine
type MachineState string

const (
	MachineStateMaintenanceMode MachineState = "MaintenanceMode"
	MachineStateConfigured      MachineState = "Configured"
	MachineStateUnreachable     MachineState = "Unreachable"
)

// checkMachineState checks the current state of the Talos machine
// Returns the machine state: MaintenanceMode, Configured, or Unreachable
func (c *external) checkMachineState(ctx context.Context, cr *v1alpha1.ConfigurationApply) MachineState {
	node := cr.Spec.ForProvider.Node

	// Use a short timeout for the connectivity check
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	// First, try to connect with insecure (maintenance mode)
	if c.canConnectInsecure(checkCtx, cr) {
		fmt.Printf("Machine %s is in maintenance mode (insecure connection succeeded)\n", node)
		return MachineStateMaintenanceMode
	}

	// Try to connect with configured credentials (if not insecure)
	clientConfig := cr.Spec.ForProvider.ClientConfiguration
	if clientConfig.ClientCertificate != "" && clientConfig.ClientCertificate != "insecure" {
		if c.canConnectWithCreds(checkCtx, cr) {
			fmt.Printf("Machine %s is configured and running (authenticated connection succeeded)\n", node)
			return MachineStateConfigured
		}
	}

	// Machine is not accessible
	fmt.Printf("Machine %s is unreachable (installing or failed)\n", node)
	return MachineStateUnreachable
}

// canConnectInsecure checks if the machine accepts insecure connections (maintenance mode)
func (c *external) canConnectInsecure(ctx context.Context, cr *v1alpha1.ConfigurationApply) bool {
	if c.canConnectInsecureFn != nil {
		return c.canConnectInsecureFn(ctx, cr)
	}

	endpoint := getConfigurationApplyEndpoint(cr)

	talosClient, err := talosclient.New(ctx,
		talosclient.WithEndpoints(endpoint),
		talosclient.WithTLSConfig(&tls.Config{
			InsecureSkipVerify: true, //nolint:gosec
		}),
	)
	if err != nil {
		return false
	}
	defer talosClient.Close() //nolint:errcheck

	// Try a simple operation - if it works without authentication, we're in maintenance mode
	// Use a timeout to avoid hanging
	checkCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	// Try to call an API - even if it fails, the connection pattern tells us the mode
	_, err = talosClient.Version(checkCtx)
	if err == nil {
		// Version succeeded with insecure connection - definitely maintenance mode
		return true
	}

	// Check error type to determine machine state
	errStr := err.Error()
	// "Unimplemented" means maintenance mode API (connection succeeded but API not available)
	if strings.Contains(errStr, "Unimplemented") || strings.Contains(errStr, "not implemented in maintenance") {
		return true
	}
	// Auth errors mean configured mode (machine requires credentials)
	if strings.Contains(errStr, "authentication") || strings.Contains(errStr, "credentials") {
		return false
	}
	// Any other error (connection refused, timeout, etc.) means unreachable
	return false
}

// canConnectWithCreds checks if the machine accepts authenticated connections (configured mode)
func (c *external) canConnectWithCreds(ctx context.Context, cr *v1alpha1.ConfigurationApply) bool {
	endpoint := getConfigurationApplyEndpoint(cr)

	tlsConfig, err := buildConfigurationApplyTLSConfig(cr.Spec.ForProvider.ClientConfiguration, cr.Spec.ForProvider.Node)
	if err != nil {
		return false
	}

	talosClient, err := talosclient.New(ctx,
		talosclient.WithTLSConfig(tlsConfig),
		talosclient.WithEndpoints(endpoint),
	)
	if err != nil {
		return false
	}
	defer talosClient.Close() //nolint:errcheck

	// Try to get version with authenticated connection
	checkCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	_, err = talosClient.Version(checkCtx)
	return err == nil
}

// applyConfigurationToNode applies a Talos configuration to the specified node
func (c *external) applyConfigurationToNode(ctx context.Context, cr *v1alpha1.ConfigurationApply) error {
	// Generate machine configuration from structured fields
	configInput, err := c.generateMachineConfigurationYAML(cr.Spec.ForProvider.MachineConfiguration)
	if err != nil {
		return errors.Wrap(err, "failed to generate machine configuration YAML")
	}

	fmt.Printf("Generated configuration YAML (length: %d bytes)\n", len(configInput))

	// For now, skip config parsing validation
	// In a complete implementation, this would validate the configuration

	endpoint := getConfigurationApplyEndpoint(cr)

	tlsConfig, maintenanceMode, err := c.buildApplyTLSConfig(ctx, cr)
	if err != nil {
		return err
	}
	if maintenanceMode {
		fmt.Printf("Machine %s is in maintenance mode; using insecure first apply\n", cr.Spec.ForProvider.Node)
	} else if tlsConfig.InsecureSkipVerify {
		fmt.Printf("Using insecure gRPC connection for maintenance mode machine\n")
	} else {
		fmt.Printf("Using secure TLS connection with client certificates\n")
	}
	talosClient, err := talosclient.New(ctx,
		talosclient.WithTLSConfig(tlsConfig),
		talosclient.WithEndpoints(endpoint),
	)
	if err != nil {
		return errors.Wrap(err, "failed to create Talos client")
	}
	defer talosClient.Close() // nolint:errcheck

	// Apply the configuration to the node
	mode, err := getConfigurationApplyMode(cr.Spec.ForProvider.ApplyMode)
	if err != nil {
		return err
	}

	_, err = talosClient.ApplyConfiguration(ctx, &machine.ApplyConfigurationRequest{
		Data: []byte(configInput),
		Mode: mode,
	})
	if err != nil {
		return errors.Wrap(err, "failed to apply configuration to Talos node")
	}

	fmt.Printf("Successfully applied configuration to node %s\n", cr.Spec.ForProvider.Node)
	return nil
}

func (c *external) buildApplyTLSConfig(ctx context.Context, cr *v1alpha1.ConfigurationApply) (*tls.Config, bool, error) {
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if c.canConnectInsecure(checkCtx, cr) {
		return &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // Insecure mode needed for Talos maintenance-mode first apply.
		}, true, nil
	}

	tlsConfig, err := buildConfigurationApplyTLSConfig(cr.Spec.ForProvider.ClientConfiguration, cr.Spec.ForProvider.Node)
	return tlsConfig, false, err
}

func buildConfigurationApplyTLSConfig(clientConfig v1alpha1.ClientConfiguration, node string) (*tls.Config, error) {
	if clientConfig.ClientCertificate == "" || clientConfig.ClientCertificate == "insecure" {
		return &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // Insecure mode needed for maintenance mode machines.
		}, nil
	}

	cert, err := tls.X509KeyPair([]byte(clientConfig.ClientCertificate), []byte(clientConfig.ClientKey))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create client certificate")
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ServerName:   node,
		MinVersion:   tls.VersionTLS12,
	}

	if clientConfig.CACertificate != "" && clientConfig.CACertificate != "insecure" {
		roots := x509.NewCertPool()
		if ok := roots.AppendCertsFromPEM([]byte(clientConfig.CACertificate)); !ok {
			return nil, errors.New("failed to parse CA certificate")
		}
		tlsConfig.RootCAs = roots
	}

	return tlsConfig, nil
}

func getConfigurationApplyEndpoint(cr *v1alpha1.ConfigurationApply) string {
	endpoint := cr.Spec.ForProvider.Node + ":50000"
	if cr.Spec.ForProvider.Endpoint != nil && *cr.Spec.ForProvider.Endpoint != "" {
		endpoint = *cr.Spec.ForProvider.Endpoint
	}

	return endpoint
}

func getConfigurationApplyMode(applyMode *string) (machine.ApplyConfigurationRequest_Mode, error) {
	if applyMode == nil || *applyMode == "" || *applyMode == "reboot" {
		return machine.ApplyConfigurationRequest_REBOOT, nil
	}

	switch *applyMode {
	case "auto":
		return machine.ApplyConfigurationRequest_AUTO, nil
	case "no_reboot":
		return machine.ApplyConfigurationRequest_NO_REBOOT, nil
	case "staged":
		return machine.ApplyConfigurationRequest_STAGED, nil
	default:
		return machine.ApplyConfigurationRequest_REBOOT, errors.Errorf("unknown configuration apply mode %q", *applyMode)
	}
}

// generateMachineConfigurationYAML converts structured configuration to Talos machine configuration YAML
//
//nolint:gocyclo // Function complexity is acceptable for config generation
func (c *external) generateMachineConfigurationYAML(config v1alpha1.MachineConfigurationSpec) (string, error) {
	// Build the YAML configuration from structured fields

	// Set defaults
	version := config.Version
	if version == "" {
		version = "v1alpha1"
	}

	// Build machine section
	machineType := config.Machine.Type
	if machineType != "controlplane" && machineType != "worker" {
		return "", errors.New("machine.type must be 'controlplane' or 'worker'")
	}

	// Build cluster networking defaults
	dnsDomain := "cluster.local"
	if config.Cluster.Network.DNSDomain != nil {
		dnsDomain = *config.Cluster.Network.DNSDomain
	}

	podSubnets := []string{"10.244.0.0/16"}
	if len(config.Cluster.Network.PodSubnets) > 0 {
		podSubnets = config.Cluster.Network.PodSubnets
	}

	serviceSubnets := []string{"10.96.0.0/12"}
	if len(config.Cluster.Network.ServiceSubnets) > 0 {
		serviceSubnets = config.Cluster.Network.ServiceSubnets
	}

	// Build kubelet section
	kubeletSection := ""
	if config.Machine.Kubelet != nil && config.Machine.Kubelet.Image != nil {
		kubeletSection = fmt.Sprintf(`  kubelet:
    image: %s
    defaultRuntimeSeccompProfileEnabled: true
    disableManifestsDirectory: true`, *config.Machine.Kubelet.Image)
	}

	// Build features section
	featuresSection := ""
	if config.Machine.Features != nil && config.Machine.Features.RBAC != nil && *config.Machine.Features.RBAC {
		featuresSection = `  features:
    rbac: true
    stableHostname: true
    apidCheckExtKeyUsage: true
    diskQuotaSupport: true`
	}

	// Build machine CA section
	caSection := ""
	if config.Machine.CA != nil && config.Machine.CA.Crt != "" {
		if machineType == "controlplane" && config.Machine.CA.Key != "" {
			// Controlplane nodes get the full CA with private key
			crtBase64 := base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(config.Machine.CA.Crt)))
			keyBase64 := base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(config.Machine.CA.Key)))

			caSection = fmt.Sprintf(`  ca:
    crt: %s
    key: %s`, crtBase64, keyBase64)
		} else {
			// Worker nodes only get the certificate in acceptedCAs (no private key)
			crtBase64 := base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(config.Machine.CA.Crt)))

			caSection = fmt.Sprintf(`  acceptedCAs:
    - crt: %s`, crtBase64)
		}
	}

	// Build cluster CA section (Kubernetes CA)
	clusterCASection := ""
	if config.Cluster.CA != nil && config.Cluster.CA.Crt != "" {
		// Base64 encode the Kubernetes CA certificate
		crtBase64 := base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(config.Cluster.CA.Crt)))

		if config.Cluster.CA.Key != "" {
			// Include both certificate and key if key is provided
			keyBase64 := base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(config.Cluster.CA.Key)))
			clusterCASection = fmt.Sprintf(`  ca:
    crt: %s
    key: %s`, crtBase64, keyBase64)
		} else {
			// Include only certificate if no key is provided (typical for worker nodes)
			clusterCASection = fmt.Sprintf(`  ca:
    crt: %s`, crtBase64)
		}
	}

	// Generate YAML configuration
	yamlConfig := fmt.Sprintf(`# Talos machine configuration generated from structured fields
version: %s
debug: false
persist: true
machine:
  type: %s
  token: %s
  install:
    disk: %s
    image: %s
    wipe: %t
%s
%s
%s
  network: {}
  sysctls: {}
  sysfs: {}
  registries: {}
cluster:
  id: %s
  secret: %s
  controlPlane:
    endpoint: %s
  clusterName: %s
  network:
    dnsDomain: %s
    podSubnets:
      - %s
    serviceSubnets:
      - %s
  token: %s
%s
  secretboxEncryptionSecret: ""
`,
		version,
		machineType,
		config.Machine.Token,
		config.Machine.Install.Disk,
		config.Machine.Install.Image,
		config.Machine.Install.Wipe != nil && *config.Machine.Install.Wipe,
		kubeletSection,
		featuresSection,
		caSection,
		config.Cluster.ID,
		config.Cluster.Secret,
		config.Cluster.ControlPlane.Endpoint,
		config.Cluster.ClusterName,
		dnsDomain,
		podSubnets[0],     // First pod subnet
		serviceSubnets[0], // First service subnet
		config.Cluster.Token,
		clusterCASection,
	)

	return yamlConfig, nil
}

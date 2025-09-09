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
	"fmt"
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	talosclient "github.com/siderolabs/talos/pkg/machinery/client"

	"github.com/crossplane/crossplane-runtime/pkg/feature"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

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

	return &external{service: svc}, nil
}

// An ExternalClient observes, then either creates, updates, or deletes an
// external resource to ensure it reflects the managed resource's desired state.
type external struct {
	// A 'client' used to connect to the external resource API. In practice this
	// would be something like an AWS SDK client.
	service interface{}
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.ConfigurationApply)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotConfigurationApply)
	}

	fmt.Printf("Observing ConfigurationApply: %s\n", cr.Name)

	// Check if configuration has been applied
	configApplied := cr.Status.AtProvider.Applied
	appliedTimeExists := true // Always true for now since we don't have this field

	// Resource exists if we have applied the configuration
	resourceExists := configApplied && appliedTimeExists

	// Check if we have a valid machine configuration input (not placeholder)
	hasValidConfig := cr.Spec.ForProvider.MachineConfigurationInput != "" &&
		!strings.Contains(cr.Spec.ForProvider.MachineConfigurationInput, "# This should be populated")

	// Resource is up to date if it exists and has valid config
	resourceUpToDate := resourceExists && hasValidConfig

	fmt.Printf("ConfigurationApply exists: %v, up to date: %v, has valid config: %v\n", resourceExists, resourceUpToDate, hasValidConfig)

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

// applyConfigurationToNode applies a Talos configuration to the specified node
func (c *external) applyConfigurationToNode(ctx context.Context, cr *v1alpha1.ConfigurationApply) error {
	// Get the machine configuration input
	configInput := cr.Spec.ForProvider.MachineConfigurationInput
	if configInput == "" || strings.Contains(configInput, "# This should be populated") {
		return errors.New("machineConfigurationInput is empty or contains placeholder text")
	}

	// For now, skip config parsing validation
	// In a complete implementation, this would validate the configuration

	// Create TLS credentials from the client configuration
	clientConfig := cr.Spec.ForProvider.ClientConfiguration
	if clientConfig.ClientCertificate == "" {
		return errors.New("clientConfiguration is required")
	}

	// Create a certificate from the provided certificates
	cert, err := tls.X509KeyPair([]byte(clientConfig.ClientCertificate), []byte(clientConfig.ClientKey))
	if err != nil {
		return errors.Wrap(err, "failed to create client certificate")
	}

	// Create TLS config
	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		ServerName:         cr.Spec.ForProvider.Node, // Use node IP as server name for now
		InsecureSkipVerify: true, // For development - should be configurable // nolint:gosec
	}

	// Create Talos client
	endpoints := []string{cr.Spec.ForProvider.Node + ":50000"} // Default Talos port
	talosClient, err := talosclient.New(ctx,
		talosclient.WithTLSConfig(tlsConfig),
		talosclient.WithEndpoints(endpoints...),
	)
	if err != nil {
		return errors.Wrap(err, "failed to create Talos client")
	}
	defer talosClient.Close() // nolint:errcheck

	// Apply the configuration to the node
	_, err = talosClient.ApplyConfiguration(ctx, &machine.ApplyConfigurationRequest{
		Data: []byte(configInput),
		Mode: machine.ApplyConfigurationRequest_NO_REBOOT, // Default to no reboot
	})
	if err != nil {
		return errors.Wrap(err, "failed to apply configuration to Talos node")
	}

	fmt.Printf("Successfully applied configuration to node %s\n", cr.Spec.ForProvider.Node)
	return nil
}

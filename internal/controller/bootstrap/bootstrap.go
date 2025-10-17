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
	"crypto/tls"
	"fmt"

	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	talosclient "github.com/siderolabs/talos/pkg/machinery/client"

	"github.com/crossplane/crossplane-runtime/pkg/feature"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	errNotBootstrap = "managed resource is not a Bootstrap custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage"
	errGetPC        = "cannot get ProviderConfig"
	errGetCreds     = "cannot get credentials"

	errNewClient = "cannot create new Service"
)

// A NoOpService does nothing.
type NoOpService struct{}

var (
	newNoOpService = func(_ []byte) (interface{}, error) { return &NoOpService{}, nil }
)

// Setup adds a controller that reconciles Bootstrap managed resources.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(v1alpha1.BootstrapGroupKind)

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
			mgr.GetClient(), o.Logger, o.MetricOptions.MRStateMetrics, &v1alpha1.BootstrapList{}, o.MetricOptions.PollStateMetricInterval,
		)
		if err := mgr.Add(stateMetricsRecorder); err != nil {
			return errors.Wrap(err, "cannot register MR state metrics recorder for kind v1alpha1.BootstrapList")
		}
	}

	r := managed.NewReconciler(mgr, resource.ManagedKind(v1alpha1.BootstrapGroupVersionKind), opts...)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o.ForControllerRuntime()).
		WithEventFilter(resource.DesiredStateChanged()).
		For(&v1alpha1.Bootstrap{}).
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
	cr, ok := mg.(*v1alpha1.Bootstrap)
	if !ok {
		return nil, errors.New(errNotBootstrap)
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
	cr, ok := mg.(*v1alpha1.Bootstrap)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotBootstrap)
	}

	fmt.Printf("Observing Bootstrap: %s\n", cr.Name)

	// Check if cluster has been bootstrapped
	clusterBootstrapped := cr.Status.AtProvider.Bootstrapped
	bootstrapTimeExists := cr.Status.AtProvider.BootstrapTime != nil

	// Resource exists if we have bootstrapped the cluster
	resourceExists := clusterBootstrapped && bootstrapTimeExists

	// Resource is up to date if it exists
	resourceUpToDate := resourceExists

	fmt.Printf("Bootstrap exists: %v, up to date: %v\n", resourceExists, resourceUpToDate)

	return managed.ExternalObservation{
		ResourceExists:    resourceExists,
		ResourceUpToDate:  resourceUpToDate,
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Bootstrap)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotBootstrap)
	}

	fmt.Printf("Bootstrapping Talos cluster on node: %s\n", cr.Spec.ForProvider.Node)

	// Bootstrap the Talos cluster
	err := c.bootstrapTalosCluster(ctx, cr)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to bootstrap Talos cluster")
	}

	// Update status
	cr.Status.AtProvider.Bootstrapped = true
	now := metav1.Now()
	cr.Status.AtProvider.BootstrapTime = &now

	return managed.ExternalCreation{
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.Bootstrap)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotBootstrap)
	}

	fmt.Printf("Updating: %+v", cr)

	return managed.ExternalUpdate{
		// Optionally return any details that may be required to connect to the
		// external resource. These will be stored as the connection secret.
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.Bootstrap)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotBootstrap)
	}

	fmt.Printf("Deleting: %+v", cr)

	return managed.ExternalDelete{}, nil
}

func (c *external) Disconnect(ctx context.Context) error {
	return nil
}

// bootstrapTalosCluster bootstraps the Talos cluster on the specified control plane node
func (c *external) bootstrapTalosCluster(ctx context.Context, cr *v1alpha1.Bootstrap) error {
	// Get client configuration
	clientConfig := cr.Spec.ForProvider.ClientConfiguration
	if clientConfig.ClientCertificate == "" {
		return errors.New("clientConfiguration is required")
	}

	// Determine endpoint - use provided endpoint or default to node:50000
	endpoint := cr.Spec.ForProvider.Node + ":50000"
	if cr.Spec.ForProvider.Endpoint != nil && *cr.Spec.ForProvider.Endpoint != "" {
		endpoint = *cr.Spec.ForProvider.Endpoint
	}

	// Handle insecure mode (when certificates are "insecure")
	var talosClient *talosclient.Client
	var err error

	if clientConfig.ClientCertificate == "insecure" || clientConfig.CACertificate == "insecure" {
		fmt.Printf("Using insecure connection to %s\n", endpoint)
		// Create insecure client
		talosClient, err = talosclient.New(ctx,
			talosclient.WithEndpoints(endpoint),
			talosclient.WithTLSConfig(&tls.Config{
				InsecureSkipVerify: true, //nolint:gosec // Insecure mode needed for maintenance mode machines
			}),
		)
	} else {
		fmt.Printf("Using secure connection to %s\n", endpoint)
		// Create a certificate from the provided certificates
		cert, certErr := tls.X509KeyPair([]byte(clientConfig.ClientCertificate), []byte(clientConfig.ClientKey))
		if certErr != nil {
			return errors.Wrap(certErr, "failed to create client certificate")
		}

		// Create TLS config
		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{cert},
			ServerName:   cr.Spec.ForProvider.Node, // Use node IP as server name
			MinVersion:   tls.VersionTLS12,
		}

		// Create Talos client
		talosClient, err = talosclient.New(ctx,
			talosclient.WithTLSConfig(tlsConfig),
			talosclient.WithEndpoints(endpoint),
		)
	}

	if err != nil {
		return errors.Wrap(err, "failed to create Talos client")
	}
	defer talosClient.Close() // nolint:errcheck

	fmt.Printf("Attempting to bootstrap Talos cluster on endpoint %s\n", endpoint)

	// Bootstrap the cluster
	err = talosClient.Bootstrap(ctx, &machine.BootstrapRequest{})
	if err != nil {
		return errors.Wrap(err, "failed to bootstrap Talos cluster")
	}

	fmt.Printf("Successfully bootstrapped Talos cluster on endpoint %s\n", endpoint)
	return nil
}

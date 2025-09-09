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
	"fmt"
	"crypto/tls"

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

	"github.com/crossplane-contrib/provider-talos/apis/cluster/v1alpha1"
	apisv1alpha1 "github.com/crossplane-contrib/provider-talos/apis/v1alpha1"
	"github.com/crossplane-contrib/provider-talos/internal/features"
)

const (
	errNotKubeconfig = "managed resource is not a Kubeconfig custom resource"
	errTrackPCUsage  = "cannot track ProviderConfig usage"
	errGetPC         = "cannot get ProviderConfig"
	errGetCreds      = "cannot get credentials"

	errNewClient = "cannot create new Service"
)

// A NoOpService does nothing.
type NoOpService struct{}

var (
	newNoOpService = func(_ []byte) (interface{}, error) { return &NoOpService{}, nil }
)

// Setup adds a controller that reconciles Kubeconfig managed resources.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(v1alpha1.KubeconfigGroupKind)

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
			mgr.GetClient(), o.Logger, o.MetricOptions.MRStateMetrics, &v1alpha1.KubeconfigList{}, o.MetricOptions.PollStateMetricInterval,
		)
		if err := mgr.Add(stateMetricsRecorder); err != nil {
			return errors.Wrap(err, "cannot register MR state metrics recorder for kind v1alpha1.KubeconfigList")
		}
	}

	r := managed.NewReconciler(mgr, resource.ManagedKind(v1alpha1.KubeconfigGroupVersionKind), opts...)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o.ForControllerRuntime()).
		WithEventFilter(resource.DesiredStateChanged()).
		For(&v1alpha1.Kubeconfig{}).
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
	cr, ok := mg.(*v1alpha1.Kubeconfig)
	if !ok {
		return nil, errors.New(errNotKubeconfig)
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
	cr, ok := mg.(*v1alpha1.Kubeconfig)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotKubeconfig)
	}

	fmt.Printf("Observing Kubeconfig: %s\n", cr.Name)

	// Check if kubeconfig has been retrieved (use existing fields)
	// Since these fields don't exist, we'll use a simple heuristic
	kubeconfigRetrieved := false // For now, always false to trigger creation
	retrievedTimeExists := true  // Always true since we don't have this field

	// Resource exists if we have retrieved the kubeconfig
	resourceExists := kubeconfigRetrieved && retrievedTimeExists

	// Resource is up to date if it exists
	resourceUpToDate := resourceExists

	fmt.Printf("Kubeconfig exists: %v, up to date: %v\n", resourceExists, resourceUpToDate)

	return managed.ExternalObservation{
		ResourceExists:   resourceExists,
		ResourceUpToDate: resourceUpToDate,
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Kubeconfig)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotKubeconfig)
	}

	fmt.Printf("Retrieving kubeconfig from node: %s\n", cr.Spec.ForProvider.Node)

	// Retrieve the kubeconfig from the Talos cluster
	kubeconfigData, err := c.retrieveKubeconfig(ctx, cr)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to retrieve kubeconfig")
	}

	// Update status
	// Note: Retrieved and LastRetrievedTime fields don't exist in the generated API, skipping

	// Return kubeconfig as connection details
	return managed.ExternalCreation{
		ConnectionDetails: managed.ConnectionDetails{
			"kubeconfig": []byte(kubeconfigData),
		},
	}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.Kubeconfig)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotKubeconfig)
	}

	fmt.Printf("Updating kubeconfig from node: %s\n", cr.Spec.ForProvider.Node)

	// Re-retrieve the kubeconfig from the Talos cluster
	kubeconfigData, err := c.retrieveKubeconfig(ctx, cr)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to retrieve kubeconfig")
	}

	// Update status
	// Note: Retrieved and LastRetrievedTime fields don't exist in the generated API, skipping

	// Return updated kubeconfig as connection details
	return managed.ExternalUpdate{
		ConnectionDetails: managed.ConnectionDetails{
			"kubeconfig": []byte(kubeconfigData),
		},
	}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.Kubeconfig)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotKubeconfig)
	}

	fmt.Printf("Deleting: %+v", cr)

	return managed.ExternalDelete{}, nil
}

func (c *external) Disconnect(ctx context.Context) error {
	return nil
}

// retrieveKubeconfig retrieves the kubeconfig from the Talos control plane node
func (c *external) retrieveKubeconfig(ctx context.Context, cr *v1alpha1.Kubeconfig) (string, error) {
	// Get client configuration
	clientConfig := cr.Spec.ForProvider.ClientConfiguration
	if clientConfig.ClientCertificate == "" {
		return "", errors.New("clientConfiguration is required")
	}

	// Create a certificate from the provided certificates
	cert, err := tls.X509KeyPair([]byte(clientConfig.ClientCertificate), []byte(clientConfig.ClientKey))
	if err != nil {
		return "", errors.Wrap(err, "failed to create client certificate")
	}

	// Create TLS config
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ServerName:   cr.Spec.ForProvider.Node, // Use node IP as server name for now
		InsecureSkipVerify: true, // For development - should be configurable
	}

	// Create Talos client
	endpoints := []string{cr.Spec.ForProvider.Node + ":50000"} // Default Talos port
	talosClient, err := talosclient.New(ctx,
		talosclient.WithTLSConfig(tlsConfig),
		talosclient.WithEndpoints(endpoints...),
	)
	if err != nil {
		return "", errors.Wrap(err, "failed to create Talos client")
	}
	defer talosClient.Close() // nolint:errcheck

	// Retrieve the kubeconfig
	kubeconfigBytes, err := talosClient.Kubeconfig(ctx)
	if err != nil {
		return "", errors.Wrap(err, "failed to retrieve kubeconfig from Talos node")
	}

	if len(kubeconfigBytes) == 0 {
		return "", errors.New("empty kubeconfig response from Talos node")
	}

	// The kubeconfig is returned as bytes, convert to string
	kubeconfigData := string(kubeconfigBytes)

	fmt.Printf("Successfully retrieved kubeconfig from node %s\n", cr.Spec.ForProvider.Node)
	return kubeconfigData, nil
}

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
	"crypto/x509"
	"fmt"
	"strings"
	"time"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	siderox509 "github.com/siderolabs/crypto/x509"
	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	talosclient "github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/crossplane/crossplane-runtime/pkg/feature"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane/crossplane-runtime/pkg/connection"
	"github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/meta"
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

	certInsecure = "insecure"
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
	// bootstrapFn allows tests to stub the non-idempotent Talos bootstrap call.
	bootstrapFn func(context.Context, *v1alpha1.Bootstrap) error
	// isBootstrappedHealthyFn allows tests to stub already-bootstrapped detection.
	isBootstrappedHealthyFn func(context.Context, *v1alpha1.Bootstrap) bool
	// newBootstrapHealthClientFn allows tests to stub Talos service state checks.
	newBootstrapHealthClientFn func(context.Context, *v1alpha1.Bootstrap) (bootstrapHealthClient, error)
}

type bootstrapHealthClient interface {
	ServiceInfo(context.Context, string, ...grpc.CallOption) ([]talosclient.ServiceInfo, error)
	Close() error
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.Bootstrap)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotBootstrap)
	}

	fmt.Printf("Observing Bootstrap: %s\n", cr.Name)

	bootstrapped := c.isBootstrappedHealthy(ctx, cr)
	if bootstrapped {
		markBootstrapped(cr, metav1.Now())
	} else if hasSuccessfulExternalCreate(cr) || cr.Status.AtProvider.Bootstrapped {
		cr.SetConditions(xpv1.Unavailable())
	}

	exists := bootstrapped || hasSuccessfulExternalCreate(cr) || cr.Status.AtProvider.Bootstrapped

	fmt.Printf("Bootstrap exists: %v, up to date: %v\n", exists, bootstrapped)

	return managed.ExternalObservation{
		ResourceExists:    exists,
		ResourceUpToDate:  bootstrapped,
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
	err := c.bootstrap(ctx, cr)
	if err != nil {
		if isBootstrapAlreadyExists(err) && c.isBootstrappedHealthy(ctx, cr) {
			markBootstrapped(cr, metav1.Now())
			return managed.ExternalCreation{
				ConnectionDetails: managed.ConnectionDetails{},
			}, nil
		}
		if isBootstrapAlreadyExists(err) {
			return managed.ExternalCreation{}, errors.Wrap(err, "bootstrap already exists but Talos-side bootstrap completion could not be verified")
		}

		return managed.ExternalCreation{}, errors.Wrap(err, "failed to bootstrap Talos cluster")
	}

	if c.isBootstrappedHealthy(ctx, cr) {
		markBootstrapped(cr, metav1.Now())
	}

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

func (c *external) bootstrap(ctx context.Context, cr *v1alpha1.Bootstrap) error {
	if c.bootstrapFn != nil {
		return c.bootstrapFn(ctx, cr)
	}

	return c.bootstrapTalosCluster(ctx, cr)
}

func hasSuccessfulExternalCreate(cr *v1alpha1.Bootstrap) bool {
	return cr.GetAnnotations()[meta.AnnotationKeyExternalCreateSucceeded] != ""
}

func markBootstrapped(cr *v1alpha1.Bootstrap, when metav1.Time) {
	cr.Status.AtProvider.Bootstrapped = true
	cr.Status.AtProvider.BootstrapTime = &when
	cr.SetConditions(xpv1.Available())
}

func isBootstrapAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	if status.Code(errors.Cause(err)) == codes.AlreadyExists || status.Code(err) == codes.AlreadyExists {
		return true
	}

	errText := err.Error()
	return strings.Contains(errText, "AlreadyExists") || strings.Contains(errText, "etcd data directory is not empty")
}

func (c *external) isBootstrappedHealthy(ctx context.Context, cr *v1alpha1.Bootstrap) bool {
	if c.isBootstrappedHealthyFn != nil {
		return c.isBootstrappedHealthyFn(ctx, cr)
	}

	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	talosClient, err := c.newBootstrapHealthClient(checkCtx, cr)
	if err != nil {
		return false
	}
	defer talosClient.Close() //nolint:errcheck

	services, err := talosClient.ServiceInfo(talosclient.WithNode(checkCtx, cr.Spec.ForProvider.Node), "etcd")
	if err != nil || len(services) == 0 {
		return false
	}

	for _, service := range services {
		if isHealthyEtcdService(service.Service) {
			return true
		}
	}

	return false
}

func (c *external) newBootstrapHealthClient(ctx context.Context, cr *v1alpha1.Bootstrap) (bootstrapHealthClient, error) {
	if c.newBootstrapHealthClientFn != nil {
		return c.newBootstrapHealthClientFn(ctx, cr)
	}

	clientConfig := cr.Spec.ForProvider.ClientConfiguration
	if clientConfig.ClientCertificate == "" {
		return nil, errors.New("clientConfiguration is required")
	}

	endpoint := getBootstrapEndpoint(cr)
	var talosClient *talosclient.Client
	var err error
	if clientConfig.ClientCertificate == certInsecure || clientConfig.CACertificate == certInsecure {
		talosClient, err = talosclient.New(ctx,
			talosclient.WithTLSConfig(&tls.Config{
				InsecureSkipVerify: true, //nolint:gosec // Insecure mode needed for maintenance-mode machines.
			}),
			talosclient.WithEndpoints(endpoint),
		)
	} else {
		talosConfig, configErr := buildBootstrapClientConfig(clientConfig)
		if configErr != nil {
			return nil, configErr
		}

		talosClient, err = talosclient.New(ctx,
			talosclient.WithConfig(talosConfig),
			talosclient.WithEndpoints(endpoint),
		)
	}
	if err != nil {
		return nil, err
	}

	return talosClient, nil
}

func isHealthyEtcdService(service *machine.ServiceInfo) bool {
	if service == nil || service.GetId() != "etcd" || service.GetState() != "Running" {
		return false
	}
	health := service.GetHealth()
	if health == nil || health.GetUnknown() || !health.GetHealthy() || hasBootstrapRequiredMessage(health.GetLastMessage()) {
		return false
	}

	for _, event := range service.GetEvents().GetEvents() {
		if hasBootstrapRequiredMessage(event.GetMsg()) {
			return false
		}
	}

	return true
}

func hasBootstrapRequiredMessage(message string) bool {
	return strings.Contains(strings.ToLower(message), "please run talosctl bootstrap")
}

// bootstrapTalosCluster bootstraps the Talos cluster on the specified control plane node
func (c *external) bootstrapTalosCluster(ctx context.Context, cr *v1alpha1.Bootstrap) error {
	// Get client configuration
	clientConfig := cr.Spec.ForProvider.ClientConfiguration
	if clientConfig.ClientCertificate == "" {
		return errors.New("clientConfiguration is required")
	}

	endpoint := getBootstrapEndpoint(cr)

	// Handle insecure mode (when certificates are "insecure")
	var talosClient *talosclient.Client
	var err error

	if clientConfig.ClientCertificate == certInsecure || clientConfig.CACertificate == certInsecure {
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
		talosConfig, configErr := buildBootstrapClientConfig(clientConfig)
		if configErr != nil {
			return configErr
		}

		// Create Talos client
		talosClient, err = talosclient.New(ctx,
			talosclient.WithConfig(talosConfig),
			talosclient.WithEndpoints(endpoint),
		)
	}

	if err != nil {
		return errors.Wrap(err, "failed to create Talos client")
	}
	defer talosClient.Close() // nolint:errcheck

	fmt.Printf("Attempting to bootstrap Talos cluster on endpoint %s\n", endpoint)

	// Bootstrap the cluster
	err = talosClient.Bootstrap(talosclient.WithNode(ctx, cr.Spec.ForProvider.Node), &machine.BootstrapRequest{})
	if err != nil {
		return errors.Wrap(err, "failed to bootstrap Talos cluster")
	}

	fmt.Printf("Successfully bootstrapped Talos cluster on endpoint %s\n", endpoint)
	return nil
}

func getBootstrapEndpoint(cr *v1alpha1.Bootstrap) string {
	endpoint := cr.Spec.ForProvider.Node + ":50000"
	if cr.Spec.ForProvider.Endpoint != nil && *cr.Spec.ForProvider.Endpoint != "" {
		endpoint = *cr.Spec.ForProvider.Endpoint
	}

	return endpoint
}

func buildBootstrapClientConfig(clientConfig v1alpha1.ClientConfiguration) (*clientconfig.Config, error) {
	if clientConfig.ClientCertificate == "" {
		return nil, errors.New("clientConfiguration.clientCertificate is required")
	}
	if clientConfig.ClientKey == "" {
		return nil, errors.New("clientConfiguration.clientKey is required")
	}
	if clientConfig.CACertificate == "" {
		return nil, errors.New("clientConfiguration.caCertificate is required")
	}

	cert, err := tls.X509KeyPair([]byte(clientConfig.ClientCertificate), []byte(clientConfig.ClientKey))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create client certificate")
	}
	if len(cert.Certificate) == 0 {
		return nil, errors.New("failed to create client certificate")
	}

	roots := x509.NewCertPool()
	if ok := roots.AppendCertsFromPEM([]byte(clientConfig.CACertificate)); !ok {
		return nil, errors.New("failed to parse CA certificate")
	}

	return clientconfig.NewConfig("dynamic", nil, []byte(clientConfig.CACertificate), &siderox509.PEMEncodedCertificateAndKey{
		Crt: []byte(clientConfig.ClientCertificate),
		Key: []byte(clientConfig.ClientKey),
	}), nil
}

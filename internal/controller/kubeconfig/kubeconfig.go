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
	"crypto/tls"
	"crypto/x509"
	"fmt"

	siderox509 "github.com/siderolabs/crypto/x509"
	talosclient "github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/feature"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
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

	connectionKeyKubeconfig        = "kubeconfig"
	connectionKeyHost              = "host"
	connectionKeyCACertificate     = "caCertificate"
	connectionKeyClientCertificate = "clientCertificate"
	connectionKeyClientKey         = "clientKey"
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

	return &external{kube: c.kube, service: svc}, nil
}

// An ExternalClient observes, then either creates, updates, or deletes an
// external resource to ensure it reflects the managed resource's desired state.
type external struct {
	// A 'client' used to connect to the external resource API. In practice this
	// would be something like an AWS SDK client.
	kube                 ctrlclient.Client
	service              interface{}
	retrieveKubeconfigFn func(context.Context, *v1alpha1.Kubeconfig) (string, error)
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.Kubeconfig)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotKubeconfig)
	}

	fmt.Printf("Observing Kubeconfig: %s\n", cr.Name)

	ref := cr.GetWriteConnectionSecretToReference()
	if ref == nil || ref.Name == "" || ref.Namespace == "" || c.kube == nil {
		fmt.Printf("Kubeconfig exists: %v, up to date: %v\n", false, false)

		return managed.ExternalObservation{
			ResourceExists:    false,
			ResourceUpToDate:  false,
			ConnectionDetails: managed.ConnectionDetails{},
		}, nil
	}

	secret := &corev1.Secret{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: ref.Namespace}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			fmt.Printf("Kubeconfig exists: %v, up to date: %v\n", false, false)

			return managed.ExternalObservation{
				ResourceExists:    false,
				ResourceUpToDate:  false,
				ConnectionDetails: managed.ConnectionDetails{},
			}, nil
		}

		return managed.ExternalObservation{}, err
	}

	kubeconfigData := secret.Data[connectionKeyKubeconfig]
	if len(kubeconfigData) == 0 {
		fmt.Printf("Kubeconfig exists: %v, up to date: %v\n", false, false)

		return managed.ExternalObservation{
			ResourceExists:    false,
			ResourceUpToDate:  false,
			ConnectionDetails: managed.ConnectionDetails{},
		}, nil
	}

	clientConfiguration, err := parseKubeconfig(string(kubeconfigData))
	if err != nil {
		return managed.ExternalObservation{}, err
	}

	cr.Status.AtProvider.KubernetesClientConfiguration = clientConfiguration
	cr.SetConditions(xpv1.Available())
	fmt.Printf("Kubeconfig exists: %v, up to date: %v\n", true, true)

	return managed.ExternalObservation{
		ResourceExists:    true,
		ResourceUpToDate:  true,
		ConnectionDetails: connectionDetails(string(kubeconfigData), clientConfiguration),
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Kubeconfig)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotKubeconfig)
	}

	fmt.Printf("Retrieving kubeconfig from node: %s\n", cr.Spec.ForProvider.Node)

	kubeconfigData, err := c.retrieveKubeconfig(ctx, cr)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to retrieve kubeconfig")
	}

	clientConfiguration, err := parseKubeconfig(kubeconfigData)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to parse kubeconfig")
	}

	return managed.ExternalCreation{
		ConnectionDetails: connectionDetails(kubeconfigData, clientConfiguration),
	}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.Kubeconfig)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotKubeconfig)
	}

	fmt.Printf("Updating kubeconfig from node: %s\n", cr.Spec.ForProvider.Node)

	kubeconfigData, err := c.retrieveKubeconfig(ctx, cr)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to retrieve kubeconfig")
	}

	clientConfiguration, err := parseKubeconfig(kubeconfigData)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to parse kubeconfig")
	}

	return managed.ExternalUpdate{
		ConnectionDetails: connectionDetails(kubeconfigData, clientConfiguration),
	}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	_, ok := mg.(*v1alpha1.Kubeconfig)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotKubeconfig)
	}

	return managed.ExternalDelete{}, nil
}

func (c *external) Disconnect(ctx context.Context) error {
	return nil
}

// retrieveKubeconfig retrieves the kubeconfig from the Talos control plane node
func (c *external) retrieveKubeconfig(ctx context.Context, cr *v1alpha1.Kubeconfig) (string, error) {
	if c.retrieveKubeconfigFn != nil {
		return c.retrieveKubeconfigFn(ctx, cr)
	}

	return retrieveKubeconfigFromTalos(ctx, cr)
}

func retrieveKubeconfigFromTalos(ctx context.Context, cr *v1alpha1.Kubeconfig) (string, error) {
	// Get client configuration
	clientConfig := cr.Spec.ForProvider.ClientConfiguration
	talosConfig, err := buildKubeconfigClientConfig(clientConfig)
	if err != nil {
		return "", err
	}

	endpoint := getKubeconfigEndpoint(cr)

	// Create Talos client
	talosClient, err := talosclient.New(ctx,
		talosclient.WithConfig(talosConfig),
		talosclient.WithEndpoints(endpoint),
	)
	if err != nil {
		return "", errors.Wrap(err, "failed to create Talos client")
	}
	defer talosClient.Close() // nolint:errcheck

	// Retrieve the kubeconfig
	kubeconfigBytes, err := talosClient.Kubeconfig(talosclient.WithNode(ctx, cr.Spec.ForProvider.Node))
	if err != nil {
		return "", errors.Wrap(err, "failed to retrieve kubeconfig from Talos node")
	}

	if len(kubeconfigBytes) == 0 {
		return "", errors.New("empty kubeconfig response from Talos node")
	}

	fmt.Printf("Successfully retrieved kubeconfig from node %s\n", cr.Spec.ForProvider.Node)

	return string(kubeconfigBytes), nil
}

func getKubeconfigEndpoint(cr *v1alpha1.Kubeconfig) string {
	endpoint := cr.Spec.ForProvider.Node + ":50000"
	if cr.Spec.ForProvider.Endpoint != nil && *cr.Spec.ForProvider.Endpoint != "" {
		endpoint = *cr.Spec.ForProvider.Endpoint
	}

	return endpoint
}

func buildKubeconfigClientConfig(clientConfig v1alpha1.ClientConfiguration) (*clientconfig.Config, error) {
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

func parseKubeconfig(kubeconfigData string) (*v1alpha1.KubernetesClientConfiguration, error) {
	config, err := clientcmd.Load([]byte(kubeconfigData))
	if err != nil {
		return nil, err
	}

	contextName, err := selectedContextName(config)
	if err != nil {
		return nil, err
	}

	contextConfig, ok := config.Contexts[contextName]
	if !ok || contextConfig == nil {
		return nil, errors.Errorf("kubeconfig context %q not found", contextName)
	}

	cluster, ok := config.Clusters[contextConfig.Cluster]
	if !ok || cluster == nil {
		return nil, errors.Errorf("kubeconfig cluster %q not found", contextConfig.Cluster)
	}

	authInfo, ok := config.AuthInfos[contextConfig.AuthInfo]
	if !ok || authInfo == nil {
		return nil, errors.Errorf("kubeconfig auth info %q not found", contextConfig.AuthInfo)
	}

	if err := validateEmbeddedClientConfiguration(cluster, authInfo); err != nil {
		return nil, err
	}

	return &v1alpha1.KubernetesClientConfiguration{
		Host:              cluster.Server,
		CACertificate:     string(cluster.CertificateAuthorityData),
		ClientCertificate: string(authInfo.ClientCertificateData),
		ClientKey:         string(authInfo.ClientKeyData),
	}, nil
}

func selectedContextName(config *clientcmdapi.Config) (string, error) {
	if config.CurrentContext != "" {
		return config.CurrentContext, nil
	}
	if len(config.Contexts) != 1 {
		return "", errors.New("kubeconfig current context is required when multiple or no contexts are present")
	}
	for name := range config.Contexts {
		return name, nil
	}

	return "", errors.New("kubeconfig current context is required when multiple or no contexts are present")
}

func validateEmbeddedClientConfiguration(cluster *clientcmdapi.Cluster, authInfo *clientcmdapi.AuthInfo) error {
	if cluster.Server == "" {
		return errors.New("kubeconfig cluster server is required")
	}
	if cluster.CertificateAuthority != "" {
		return errors.New("kubeconfig certificate-authority file references are not supported")
	}
	if len(cluster.CertificateAuthorityData) == 0 {
		return errors.New("kubeconfig cluster certificate-authority-data is required")
	}
	if authInfo.ClientCertificate != "" {
		return errors.New("kubeconfig client-certificate file references are not supported")
	}
	if len(authInfo.ClientCertificateData) == 0 {
		return errors.New("kubeconfig client-certificate-data is required")
	}
	if authInfo.ClientKey != "" {
		return errors.New("kubeconfig client-key file references are not supported")
	}
	if len(authInfo.ClientKeyData) == 0 {
		return errors.New("kubeconfig client-key-data is required")
	}

	return nil
}

func connectionDetails(kubeconfigData string, clientConfiguration *v1alpha1.KubernetesClientConfiguration) managed.ConnectionDetails {
	details := connectionDetailsFromClientConfiguration(clientConfiguration)
	details[connectionKeyKubeconfig] = []byte(kubeconfigData)

	return details
}

func connectionDetailsFromClientConfiguration(clientConfiguration *v1alpha1.KubernetesClientConfiguration) managed.ConnectionDetails {
	if clientConfiguration == nil {
		return managed.ConnectionDetails{}
	}

	details := managed.ConnectionDetails{
		connectionKeyHost:              []byte(clientConfiguration.Host),
		connectionKeyCACertificate:     []byte(clientConfiguration.CACertificate),
		connectionKeyClientCertificate: []byte(clientConfiguration.ClientCertificate),
		connectionKeyClientKey:         []byte(clientConfiguration.ClientKey),
	}

	kubeconfigData, err := kubeconfigFromClientConfiguration(clientConfiguration)
	if err == nil {
		details[connectionKeyKubeconfig] = kubeconfigData
	}

	return details
}

func kubeconfigFromClientConfiguration(clientConfiguration *v1alpha1.KubernetesClientConfiguration) ([]byte, error) {
	config := clientcmdapi.NewConfig()
	config.CurrentContext = "default"
	config.Clusters["default"] = &clientcmdapi.Cluster{
		Server:                   clientConfiguration.Host,
		CertificateAuthorityData: []byte(clientConfiguration.CACertificate),
	}
	config.AuthInfos["default"] = &clientcmdapi.AuthInfo{
		ClientCertificateData: []byte(clientConfiguration.ClientCertificate),
		ClientKeyData:         []byte(clientConfiguration.ClientKey),
	}
	config.Contexts["default"] = &clientcmdapi.Context{
		Cluster:  "default",
		AuthInfo: "default",
	}

	return clientcmd.Write(*config)
}

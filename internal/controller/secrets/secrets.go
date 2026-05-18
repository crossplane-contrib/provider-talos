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
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/crossplane/crossplane-runtime/pkg/feature"

	"github.com/pkg/errors"
	siderox509 "github.com/siderolabs/crypto/x509"
	talosconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/siderolabs/talos/pkg/machinery/role"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	talossecrets "github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

const (
	errNotSecrets   = "managed resource is not a Secrets custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage"
	errGetPC        = "cannot get ProviderConfig"
	errGetCreds     = "cannot get credentials"

	errNewClient = "cannot create new Service"

	connectionKeyMachineSecrets       = "machine_secrets"
	connectionKeyMachineSecretsBundle = "machine_secrets_bundle"
	connectionKeyClientConfiguration  = "client_configuration"
	connectionKeyCACertificate        = "ca_certificate"
	connectionKeyClientCertificate    = "client_certificate"
	connectionKeyClientKey            = "client_key"
	connectionKeyTalosConfig          = "talos_config"
)

// TalosSecretsService manages Talos machine secrets
type TalosSecretsService struct {
	credentials []byte
}

// GenerateSecrets generates new Talos machine secrets
type GeneratedSecrets struct {
	Cluster     *talossecrets.Cluster
	Secrets     *talossecrets.Bundle
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

	return &external{kube: c.kube, service: svc.(*TalosSecretsService)}, nil
}

// An ExternalClient observes, then either creates, updates, or deletes an
// external resource to ensure it reflects the managed resource's desired state.
type external struct {
	kube    client.Client
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

	var connectionDetails managed.ConnectionDetails
	if cr.Status.AtProvider.Generated {
		var err error
		connectionDetails, err = c.connectionDetailsFromSecret(ctx, cr)
		if err != nil {
			return managed.ExternalObservation{}, err
		}
	} else {
		if cr.Spec.WriteConnectionSecretToReference == nil {
			return managed.ExternalObservation{}, errors.New("writeConnectionSecretToRef is required to persist generated machine secrets")
		}
		generatedSecrets, err := c.generateMachineSecrets(cr.Spec.ForProvider.TalosVersion)
		if err != nil {
			return managed.ExternalObservation{}, errors.Wrap(err, "failed to generate machine secrets")
		}
		connectionDetails, err = connectionDetailsFromGeneratedSecrets(generatedSecrets)
		if err != nil {
			return managed.ExternalObservation{}, err
		}
		populateStatusMetadata(cr, connectionDetails)
	}

	// Set Ready condition
	cr.SetConditions(xpv1.Available())

	return managed.ExternalObservation{
		ResourceExists:    true,
		ResourceUpToDate:  true,
		ConnectionDetails: connectionDetails,
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Secrets)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotSecrets)
	}
	if cr.Spec.WriteConnectionSecretToReference == nil {
		return managed.ExternalCreation{}, errors.New("writeConnectionSecretToRef is required to persist generated machine secrets")
	}

	// Generate new machine secrets using Talos SDK
	generatedSecrets, err := c.generateMachineSecrets(cr.Spec.ForProvider.TalosVersion)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to generate machine secrets")
	}
	connectionDetails, err := connectionDetailsFromGeneratedSecrets(generatedSecrets)
	if err != nil {
		return managed.ExternalCreation{}, err
	}
	populateStatusMetadata(cr, connectionDetails)

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
	Bundle              string
	MachineSecrets      *v1alpha1.MachineSecrets
	ClusterSecrets      string
	KubernetesSecrets   string
	TrustdInfo          string
	ClientConfiguration *v1alpha1.ClientConfiguration
}

// generateMachineSecrets generates new Talos machine secrets using the Talos SDK
func (c *external) generateMachineSecrets(talosVersion *string) (*GeneratedSecretsResult, error) {
	versionContract, err := parseVersionContract(talosVersion)
	if err != nil {
		return nil, err
	}

	// Generate machine secrets bundle using current time
	clock := talossecrets.NewClock()
	secretsBundle, err := talossecrets.NewBundle(clock, versionContract)
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate secrets bundle")
	}
	bundleJSON, err := json.Marshal(secretsBundle)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal secrets bundle")
	}
	machineSecrets, err := SecretsBundleToMachineSecrets(secretsBundle)
	if err != nil {
		return nil, err
	}

	clientConfiguration, err := GenerateClientConfiguration(secretsBundle, constants.TalosAPIDefaultCertificateValidityDuration)
	if err != nil {
		return nil, err
	}

	clusterSecretsJSON, err := marshalClusterSecrets(secretsBundle)
	if err != nil {
		return nil, err
	}

	kubernetesSecretsJSON, err := marshalKubernetesSecrets(secretsBundle)
	if err != nil {
		return nil, err
	}

	trustdInfoJSON, err := marshalTrustdInfo(secretsBundle)
	if err != nil {
		return nil, err
	}

	return &GeneratedSecretsResult{
		Bundle:              string(bundleJSON),
		MachineSecrets:      machineSecrets,
		ClusterSecrets:      clusterSecretsJSON,
		KubernetesSecrets:   kubernetesSecretsJSON,
		TrustdInfo:          trustdInfoJSON,
		ClientConfiguration: clientConfiguration,
	}, nil
}

func parseVersionContract(talosVersion *string) (*talosconfig.VersionContract, error) {
	if talosVersion == nil || *talosVersion == "" {
		return nil, nil
	}

	versionContract, err := talosconfig.ParseContractFromVersion(*talosVersion)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse talos version")
	}

	return versionContract, nil
}

func marshalClusterSecrets(bundle *talossecrets.Bundle) (string, error) {
	clusterSecretsJSON, err := json.Marshal(map[string]interface{}{
		"id":     bundle.Cluster.ID,
		"secret": bundle.Cluster.Secret,
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal cluster secrets")
	}

	return string(clusterSecretsJSON), nil
}

func marshalKubernetesSecrets(bundle *talossecrets.Bundle) (string, error) {
	kubernetesSecretsJSON, err := json.Marshal(map[string]interface{}{
		"ca": map[string]interface{}{
			"crt": string(bundle.Certs.K8s.Crt),
			"key": string(bundle.Certs.K8s.Key),
		},
		"aggregatorCA": map[string]interface{}{
			"crt": string(bundle.Certs.K8sAggregator.Crt),
			"key": string(bundle.Certs.K8sAggregator.Key),
		},
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal kubernetes secrets")
	}

	return string(kubernetesSecretsJSON), nil
}

func marshalTrustdInfo(bundle *talossecrets.Bundle) (string, error) {
	trustdInfoJSON, err := json.Marshal(map[string]interface{}{"token": bundle.TrustdInfo.Token})
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal trustd info")
	}

	return string(trustdInfoJSON), nil
}

func populateStatusMetadata(cr *v1alpha1.Secrets, connectionDetails managed.ConnectionDetails) {
	cr.Status.AtProvider.Generated = true
	if cr.Status.AtProvider.GeneratedTime == nil {
		now := metav1.Now()
		cr.Status.AtProvider.GeneratedTime = &now
	}
	cr.Status.AtProvider.MachineSecretsHash = hashConnectionDetail(connectionDetails, connectionKeyMachineSecretsBundle)
	cr.Status.AtProvider.ClientConfigurationHash = hashConnectionDetail(connectionDetails, connectionKeyClientConfiguration)
	cr.Status.AtProvider.TalosConfigHash = hashConnectionDetail(connectionDetails, connectionKeyTalosConfig)
}

func connectionDetailsFromGeneratedSecrets(generatedSecrets *GeneratedSecretsResult) (managed.ConnectionDetails, error) {
	connectionDetails := managed.ConnectionDetails{}

	if generatedSecrets.ClientConfiguration != nil {
		clientConfigurationJSON, err := marshalBase64ClientConfiguration(generatedSecrets.ClientConfiguration)
		if err != nil {
			return nil, err
		}

		connectionDetails[connectionKeyCACertificate] = []byte(generatedSecrets.ClientConfiguration.CACertificate)
		connectionDetails[connectionKeyClientCertificate] = []byte(generatedSecrets.ClientConfiguration.ClientCertificate)
		connectionDetails[connectionKeyClientKey] = []byte(generatedSecrets.ClientConfiguration.ClientKey)
		connectionDetails[connectionKeyClientConfiguration] = clientConfigurationJSON

		talosConfig, err := marshalTalosConfig(generatedSecrets.ClientConfiguration)
		if err != nil {
			return nil, err
		}
		connectionDetails[connectionKeyTalosConfig] = talosConfig
	}

	if generatedSecrets.MachineSecrets != nil {
		structuredJSON, err := json.Marshal(generatedSecrets.MachineSecrets)
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal structured machine secrets")
		}
		connectionDetails[connectionKeyMachineSecrets] = structuredJSON
	}
	if generatedSecrets.Bundle != "" {
		connectionDetails[connectionKeyMachineSecretsBundle] = []byte(generatedSecrets.Bundle)
	}

	return connectionDetails, nil
}

func (c *external) connectionDetailsFromSecret(ctx context.Context, cr *v1alpha1.Secrets) (managed.ConnectionDetails, error) {
	if cr.Spec.WriteConnectionSecretToReference == nil {
		return nil, errors.New("generated machine secrets require writeConnectionSecretToRef to reload connection details")
	}
	if c.kube == nil {
		return nil, errors.New("cannot reload generated machine secrets without Kubernetes client")
	}

	ref := cr.Spec.WriteConnectionSecretToReference
	secret := &corev1.Secret{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: ref.Namespace}, secret); err != nil {
		return nil, errors.Wrapf(err, "cannot get generated machine secrets connection secret %s/%s", ref.Namespace, ref.Name)
	}

	connectionDetails := managed.ConnectionDetails{}
	for _, key := range requiredConnectionKeys() {
		value, ok := secret.Data[key]
		if !ok {
			return nil, errors.Errorf("generated machine secrets connection secret %s/%s is missing key %q", ref.Namespace, ref.Name, key)
		}
		connectionDetails[key] = value
	}

	return connectionDetails, nil
}

func requiredConnectionKeys() []string {
	return []string{
		connectionKeyMachineSecrets,
		connectionKeyMachineSecretsBundle,
		connectionKeyClientConfiguration,
		connectionKeyCACertificate,
		connectionKeyClientCertificate,
		connectionKeyClientKey,
		connectionKeyTalosConfig,
	}
}

func hashConnectionDetail(connectionDetails managed.ConnectionDetails, key string) string {
	hash := sha256.Sum256(connectionDetails[key])
	return hex.EncodeToString(hash[:])
}

func marshalBase64ClientConfiguration(clientConfiguration *v1alpha1.ClientConfiguration) ([]byte, error) {
	return json.Marshal(v1alpha1.ClientConfiguration{
		CACertificate:     base64.StdEncoding.EncodeToString([]byte(clientConfiguration.CACertificate)),
		ClientCertificate: base64.StdEncoding.EncodeToString([]byte(clientConfiguration.ClientCertificate)),
		ClientKey:         base64.StdEncoding.EncodeToString([]byte(clientConfiguration.ClientKey)),
	})
}

func marshalTalosConfig(clientConfiguration *v1alpha1.ClientConfiguration) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"context": "default",
		"contexts": map[string]interface{}{
			"default": map[string]interface{}{
				"ca":  clientConfiguration.CACertificate,
				"crt": clientConfiguration.ClientCertificate,
				"key": clientConfiguration.ClientKey,
			},
		},
	})
}

// SecretsBundleToMachineSecrets converts a Talos SDK bundle to the public structured contract.
func SecretsBundleToMachineSecrets(bundle *talossecrets.Bundle) (*v1alpha1.MachineSecrets, error) {
	if err := validateBundleForStructuredSecrets(bundle); err != nil {
		return nil, err
	}

	return &v1alpha1.MachineSecrets{
		Cluster: v1alpha1.MachineSecretsCluster{
			ID:     bundle.Cluster.ID,
			Secret: bundle.Cluster.Secret,
		},
		Secrets: v1alpha1.MachineSecretsSecrets{
			BootstrapToken:            bundle.Secrets.BootstrapToken,
			SecretboxEncryptionSecret: bundle.Secrets.SecretboxEncryptionSecret,
			AESCBCEncryptionSecret:    bundle.Secrets.AESCBCEncryptionSecret,
		},
		TrustdInfo: v1alpha1.MachineSecretsTrustdInfo{Token: bundle.TrustdInfo.Token},
		Certs: v1alpha1.MachineSecretsCerts{
			Etcd:              encodeCertificateAndKey(bundle.Certs.Etcd),
			K8s:               encodeCertificateAndKey(bundle.Certs.K8s),
			K8sAggregator:     encodeCertificateAndKey(bundle.Certs.K8sAggregator),
			K8sServiceAccount: encodeKey(bundle.Certs.K8sServiceAccount),
			OS:                encodeCertificateAndKey(bundle.Certs.OS),
		},
	}, nil
}

func validateBundleForStructuredSecrets(bundle *talossecrets.Bundle) error {
	if bundle == nil || bundle.Cluster == nil || bundle.Secrets == nil || bundle.TrustdInfo == nil || bundle.Certs == nil {
		return errors.New("machine secrets bundle is incomplete")
	}

	return validateBundleCerts(bundle.Certs)
}

func validateBundleCerts(certs *talossecrets.Certs) error {
	if certs.Etcd == nil || certs.K8s == nil || certs.K8sAggregator == nil || certs.K8sServiceAccount == nil || certs.OS == nil {
		return errors.New("machine secrets bundle certificates are incomplete")
	}

	return nil
}

// MachineSecretsToSecretsBundle converts the public structured contract back to a Talos SDK bundle.
func MachineSecretsToSecretsBundle(machineSecrets *v1alpha1.MachineSecrets) (*talossecrets.Bundle, error) {
	if machineSecrets == nil {
		return nil, errors.New("machine secrets are nil")
	}

	etcd, err := decodeCertificateAndKey(machineSecrets.Certs.Etcd)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode etcd certificate and key")
	}
	k8s, err := decodeCertificateAndKey(machineSecrets.Certs.K8s)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode k8s certificate and key")
	}
	k8sAggregator, err := decodeCertificateAndKey(machineSecrets.Certs.K8sAggregator)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode k8s aggregator certificate and key")
	}
	k8sServiceAccount, err := decodeKey(machineSecrets.Certs.K8sServiceAccount)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode k8s service account key")
	}
	os, err := decodeCertificateAndKey(machineSecrets.Certs.OS)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode os certificate and key")
	}

	bundle := &talossecrets.Bundle{
		Clock: talossecrets.NewClock(),
		Cluster: &talossecrets.Cluster{
			ID:     machineSecrets.Cluster.ID,
			Secret: machineSecrets.Cluster.Secret,
		},
		Secrets: &talossecrets.Secrets{
			BootstrapToken:            machineSecrets.Secrets.BootstrapToken,
			AESCBCEncryptionSecret:    machineSecrets.Secrets.AESCBCEncryptionSecret,
			SecretboxEncryptionSecret: machineSecrets.Secrets.SecretboxEncryptionSecret,
		},
		TrustdInfo: &talossecrets.TrustdInfo{Token: machineSecrets.TrustdInfo.Token},
		Certs: &talossecrets.Certs{
			Etcd:              etcd,
			K8s:               k8s,
			K8sAggregator:     k8sAggregator,
			K8sServiceAccount: k8sServiceAccount,
			OS:                os,
		},
	}

	if err := bundle.Validate(); err != nil {
		return nil, errors.Wrap(err, "structured machine secrets are invalid")
	}

	return bundle, nil
}

// GenerateClientConfiguration creates a Talos API admin client certificate from the OS CA.
func GenerateClientConfiguration(bundle *talossecrets.Bundle, ttl time.Duration) (*v1alpha1.ClientConfiguration, error) {
	if bundle == nil || bundle.Certs == nil || bundle.Certs.OS == nil {
		return nil, errors.New("machine secrets bundle does not contain an OS CA")
	}

	clientCertificate, err := bundle.GenerateTalosAPIClientCertificateWithTTL(role.MakeSet(role.Admin), ttl)
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate Talos API client certificate")
	}

	return &v1alpha1.ClientConfiguration{
		CACertificate:     string(bundle.Certs.OS.Crt),
		ClientCertificate: string(clientCertificate.Crt),
		ClientKey:         string(clientCertificate.Key),
	}, nil
}

func encodeCertificateAndKey(certificateAndKey *siderox509.PEMEncodedCertificateAndKey) v1alpha1.MachineSecretsCertificateAndKey {
	return v1alpha1.MachineSecretsCertificateAndKey{
		Cert: base64.StdEncoding.EncodeToString(certificateAndKey.Crt),
		Key:  base64.StdEncoding.EncodeToString(certificateAndKey.Key),
	}
}

func encodeKey(key *siderox509.PEMEncodedKey) v1alpha1.MachineSecretsKey {
	return v1alpha1.MachineSecretsKey{Key: base64.StdEncoding.EncodeToString(key.Key)}
}

func decodeCertificateAndKey(certificateAndKey v1alpha1.MachineSecretsCertificateAndKey) (*siderox509.PEMEncodedCertificateAndKey, error) {
	cert, err := base64.StdEncoding.DecodeString(certificateAndKey.Cert)
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(certificateAndKey.Key)
	if err != nil {
		return nil, err
	}

	return &siderox509.PEMEncodedCertificateAndKey{Crt: cert, Key: key}, nil
}

func decodeKey(key v1alpha1.MachineSecretsKey) (*siderox509.PEMEncodedKey, error) {
	decoded, err := base64.StdEncoding.DecodeString(key.Key)
	if err != nil {
		return nil, err
	}

	return &siderox509.PEMEncodedKey{Key: decoded}, nil
}

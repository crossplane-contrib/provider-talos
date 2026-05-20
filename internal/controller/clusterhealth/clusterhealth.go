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
	"crypto/tls"
	"crypto/x509"
	stderrors "errors"
	"fmt"
	"io"
	"strings"
	"time"

	siderox509 "github.com/siderolabs/crypto/x509"
	clusterapi "github.com/siderolabs/talos/pkg/machinery/api/cluster"
	talosclient "github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"google.golang.org/grpc/status"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/connection"
	"github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/feature"
	"github.com/crossplane/crossplane-runtime/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/crossplane/crossplane-runtime/pkg/statemetrics"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane-contrib/provider-talos/apis/cluster/v1alpha1"
	apisv1alpha1 "github.com/crossplane-contrib/provider-talos/apis/v1alpha1"
	"github.com/crossplane-contrib/provider-talos/internal/features"
)

const (
	errNotClusterHealth = "managed resource is not a ClusterHealth custom resource"
	errTrackPCUsage     = "cannot track ProviderConfig usage"
	errGetPC            = "cannot get ProviderConfig"
	errGetCreds         = "cannot get credentials"
	errNewClient        = "cannot create new Service"
)

// NoOpService does nothing.
type NoOpService struct{}

var newNoOpService = func(_ []byte) (interface{}, error) { return &NoOpService{}, nil }

// Setup adds a controller that reconciles ClusterHealth managed resources.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(v1alpha1.ClusterHealthGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), apisv1alpha1.StoreConfigGroupVersionKind))
	}

	opts := []managed.ReconcilerOption{
		managed.WithExternalConnecter(&connector{kube: mgr.GetClient(), usage: resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}), newServiceFn: newNoOpService}),
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
		stateMetricsRecorder := statemetrics.NewMRStateRecorder(mgr.GetClient(), o.Logger, o.MetricOptions.MRStateMetrics, &v1alpha1.ClusterHealthList{}, o.MetricOptions.PollStateMetricInterval)
		if err := mgr.Add(stateMetricsRecorder); err != nil {
			return errors.Wrap(err, "cannot register MR state metrics recorder for kind v1alpha1.ClusterHealthList")
		}
	}

	r := managed.NewReconciler(mgr, resource.ManagedKind(v1alpha1.ClusterHealthGroupVersionKind), opts...)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o.ForControllerRuntime()).
		WithEventFilter(resource.DesiredStateChanged()).
		For(&v1alpha1.ClusterHealth{}).
		Complete(ratelimiter.NewReconciler(name, r, o.GlobalRateLimiter))
}

type connector struct {
	kube         ctrlclient.Client
	usage        resource.Tracker
	newServiceFn func(creds []byte) (interface{}, error)
}

func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.ClusterHealth)
	if !ok {
		return nil, errors.New(errNotClusterHealth)
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

type external struct {
	kube                 ctrlclient.Client
	service              interface{}
	checkClusterHealthFn func(context.Context, *v1alpha1.ClusterHealth) (bool, string, error)
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.ClusterHealth)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotClusterHealth)
	}

	now := metav1.Now()
	cr.Status.AtProvider.LastCheckTime = &now
	cr.Status.AtProvider.CheckedControlPlaneNodes = len(cr.Spec.ForProvider.ControlPlaneNodes)
	cr.Status.AtProvider.CheckedWorkerNodes = len(cr.Spec.ForProvider.WorkerNodes)

	healthy, message, err := c.checkClusterHealth(ctx, cr)
	if err != nil {
		cr.Status.AtProvider.Healthy = false
		cr.Status.AtProvider.LastMessage = err.Error()
		cr.SetConditions(xpv1.Unavailable())
		return managed.ExternalObservation{ResourceExists: true, ResourceUpToDate: false, ConnectionDetails: managed.ConnectionDetails{}}, err
	}

	cr.Status.AtProvider.Healthy = healthy
	cr.Status.AtProvider.LastMessage = message
	if healthy {
		cr.Status.AtProvider.LastHealthyTime = &now
		cr.SetConditions(xpv1.Available())
	} else {
		cr.SetConditions(xpv1.Unavailable())
	}

	return managed.ExternalObservation{ResourceExists: true, ResourceUpToDate: healthy, ConnectionDetails: managed.ConnectionDetails{}}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	if _, ok := mg.(*v1alpha1.ClusterHealth); !ok {
		return managed.ExternalCreation{}, errors.New(errNotClusterHealth)
	}
	return managed.ExternalCreation{ConnectionDetails: managed.ConnectionDetails{}}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	if _, ok := mg.(*v1alpha1.ClusterHealth); !ok {
		return managed.ExternalUpdate{}, errors.New(errNotClusterHealth)
	}
	return managed.ExternalUpdate{ConnectionDetails: managed.ConnectionDetails{}}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	if _, ok := mg.(*v1alpha1.ClusterHealth); !ok {
		return managed.ExternalDelete{}, errors.New(errNotClusterHealth)
	}
	return managed.ExternalDelete{}, nil
}

func (c *external) Disconnect(ctx context.Context) error { return nil }

func (c *external) checkClusterHealth(ctx context.Context, cr *v1alpha1.ClusterHealth) (bool, string, error) {
	if c.checkClusterHealthFn != nil {
		return c.checkClusterHealthFn(ctx, cr)
	}
	return checkClusterHealth(ctx, cr)
}

func checkClusterHealth(ctx context.Context, cr *v1alpha1.ClusterHealth) (bool, string, error) {
	if err := validateClusterHealthSpec(cr); err != nil {
		return false, "", err
	}
	cfg, err := buildClusterHealthClientConfig(cr.Spec.ForProvider.ClientConfiguration)
	if err != nil {
		return false, "", err
	}

	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	client, message := newClusterHealthClient(checkCtx, cfg, cr.Spec.ForProvider.Endpoints)
	if client == nil {
		return false, message, nil
	}
	defer client.Close() //nolint:errcheck

	if cr.Spec.ForProvider.SkipKubernetesChecks != nil && *cr.Spec.ForProvider.SkipKubernetesChecks {
		healthy, message := checkNodeServices(checkCtx, client, allNodes(cr))
		return healthy, message, nil
	}

	healthy, message := checkFullClusterHealth(checkCtx, client, cr)
	return healthy, message, nil
}

func newClusterHealthClient(ctx context.Context, cfg *clientconfig.Config, endpoints []string) (*talosclient.Client, string) {
	client, err := talosclient.New(ctx, talosclient.WithConfig(cfg), talosclient.WithEndpoints(endpoints...))
	if err != nil {
		return nil, fmt.Sprintf("waiting for Talos API: %v", err)
	}

	return client, ""
}

func allNodes(cr *v1alpha1.ClusterHealth) []string {
	nodes := append([]string{}, cr.Spec.ForProvider.ControlPlaneNodes...)

	return append(nodes, cr.Spec.ForProvider.WorkerNodes...)
}

func checkFullClusterHealth(ctx context.Context, client *talosclient.Client, cr *v1alpha1.ClusterHealth) (bool, string) {
	stream, err := client.ClusterHealthCheck(ctx, 10*time.Second, &clusterapi.ClusterInfo{ControlPlaneNodes: cr.Spec.ForProvider.ControlPlaneNodes, WorkerNodes: cr.Spec.ForProvider.WorkerNodes})
	if err != nil {
		return false, fmt.Sprintf("waiting for cluster health: %v", err)
	}

	last := "waiting for cluster health"
	for {
		progress, err := stream.Recv()
		if err == nil {
			last = lastHealthMessage(last, progress)
			continue
		}
		if stderrors.Is(err, io.EOF) {
			return true, "cluster is healthy"
		}
		if status.Code(err).String() != "OK" {
			return false, fmt.Sprintf("%s: %v", last, err)
		}
		return false, err.Error()
	}
}

func lastHealthMessage(current string, progress *clusterapi.HealthCheckProgress) string {
	if msg := strings.TrimSpace(progress.GetMessage()); msg != "" {
		return msg
	}

	return current
}

func checkNodeServices(ctx context.Context, client *talosclient.Client, nodes []string) (bool, string) {
	for _, node := range nodes {
		infos, err := client.ServiceInfo(talosclient.WithNode(ctx, node), "kubelet")
		if err != nil {
			return false, fmt.Sprintf("waiting for node %s: %v", node, err)
		}
		if len(infos) == 0 {
			return false, fmt.Sprintf("waiting for kubelet service on node %s", node)
		}
		for _, info := range infos {
			svc := info.Service
			if svc == nil || svc.GetState() != "Running" || svc.GetHealth() == nil || !svc.GetHealth().GetHealthy() {
				return false, fmt.Sprintf("waiting for kubelet service on node %s", node)
			}
		}
	}
	return true, "Talos node checks passed"
}

func validateClusterHealthSpec(cr *v1alpha1.ClusterHealth) error {
	if len(cr.Spec.ForProvider.Endpoints) == 0 {
		return errors.New("endpoints is required")
	}
	if len(cr.Spec.ForProvider.ControlPlaneNodes) == 0 {
		return errors.New("controlPlaneNodes is required")
	}
	return nil
}

func buildClusterHealthClientConfig(clientConfig v1alpha1.ClientConfiguration) (*clientconfig.Config, error) {
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
	return clientconfig.NewConfig("dynamic", nil, []byte(clientConfig.CACertificate), &siderox509.PEMEncodedCertificateAndKey{Crt: []byte(clientConfig.ClientCertificate), Key: []byte(clientConfig.ClientKey)}), nil
}

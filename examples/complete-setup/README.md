# Complete Talos Cluster Setup Example

This example demonstrates the complete workflow for setting up a Talos machine using the Crossplane provider.

## Prerequisites

- A Talos machine in maintenance mode (accessible at the specified IP)
- Crossplane installed in your Kubernetes cluster
- provider-talos installed

## Resources

This example creates three resources in sequence:

1. **Secrets** - Generates cluster secrets locally and writes them to a connection secret
2. **Configuration** - Generates machine configuration using `machineSecretsRef`
3. **ConfigurationApply** - Applies the configuration to a real Talos machine

## Workflow

### Step 1: Create ProviderConfig

```bash
kubectl apply -f providerconfig.yaml
```

### Step 2: Generate Secrets

```bash
kubectl apply -f secrets.yaml
```

Wait for SYNCED=True:
```bash
kubectl get secrets.machine.talos.crossplane.io cluster-secrets
```

### Step 3: Generate Configuration

```bash
kubectl apply -f configuration.yaml
```

Wait for READY=True:
```bash
kubectl get configurations.machine.talos.crossplane.io worker-config
```

### Step 4: Apply Configuration to Machine

**Note:** Update the `node` field in the ConfigurationApply manifests with your Talos machine IP.

Two example manifests are provided. Both target machines in maintenance mode (using `clientConfiguration: insecure`); pick the one matching the role of the node you are provisioning:

For a worker node:
```bash
kubectl apply -f configurationapply-worker.yaml
```

For a controlplane node:
```bash
kubectl apply -f configurationapply-controlplane.yaml
```

For a node that already has certificates (configured mode), copy one of the manifests above and replace the `clientConfiguration` `caCertificate`, `clientCertificate`, and `clientKey` values with the raw PEM connection details produced by the `Secrets` resource:
```bash
kubectl get secret cluster-talos-secrets -o jsonpath='{.data.ca_certificate}' | base64 -d
kubectl get secret cluster-talos-secrets -o jsonpath='{.data.client_certificate}' | base64 -d
kubectl get secret cluster-talos-secrets -o jsonpath='{.data.client_key}' | base64 -d
```

The `machine_secrets` connection detail is the canonical structured JSON contract for compositions. The `machine_secrets_bundle` key is retained only for compatibility with consumers that need the native Talos SDK bundle JSON.

## Verification

Check the status of all resources:
```bash
kubectl get secrets.machine.talos.crossplane.io
kubectl get configurations.machine.talos.crossplane.io
kubectl get configurationapplies.machine.talos.crossplane.io
```

All resources should show SYNCED=True or READY=True.

After bootstrapping a control plane, use `examples/cluster/clusterhealth.yaml` as a readiness gate before retrieving kubeconfig with `examples/cluster/kubeconfig.yaml` or creating downstream Kubernetes resources.

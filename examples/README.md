# Talos Provider Examples

This directory contains comprehensive examples for all Talos Crossplane Provider resources.

## Quick Start

1. **Install the Provider** (ensure it's properly packaged)
2. **Configure Provider Authentication** using `provider/config.yaml`
3. **Deploy Resources** using the examples below

## Resource Types

### Core Configuration
- `provider/config.yaml` - Provider configuration and credentials
- `storeconfig/vault.yaml` - External secret store configuration

### Image Management
- `image/factoryschematic.yaml` - Custom Talos image creation via Image Factory

### Machine Configuration  
- `machine/secrets.yaml` - Generate cluster machine secrets
- `machine/controlplane-configuration.yaml` - Control plane machine configuration
- `machine/configuration.yaml` - Worker machine configuration
- `machine/configurationapply.yaml` - Apply configuration to nodes
- `machine/bootstrap.yaml` - Bootstrap cluster on control plane node

### Cluster Operations
- `cluster/kubeconfig.yaml` - Retrieve cluster kubeconfig

## Complete Workflows

### Single Node Cluster
```bash
kubectl apply -f examples/single-node-cluster.yaml
```

This creates a hybrid control-plane/worker node that can run workloads.

### Multi-Node Cluster
```bash
kubectl apply -f examples/complete-workflow.yaml
```

This demonstrates the full lifecycle:
1. Generate machine secrets
2. Create custom image (optional)
3. Generate machine configurations
4. Apply configurations to nodes  
5. Bootstrap cluster
6. Retrieve kubeconfig

## Usage Notes

### Certificate Management
Most examples show placeholder certificate values (`LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0t...`). In practice, these should:

1. **For Secrets resources**: Be generated automatically and stored in connection secrets
2. **For other resources**: Reference the actual certificate data from the secrets

### Example Certificate Extraction
```bash
# Extract certificates from generated secrets
kubectl get secret talos-cluster-secrets -o jsonpath='{.data.ca_certificate}' | base64 -d
kubectl get secret talos-cluster-secrets -o jsonpath='{.data.client_certificate}' | base64 -d  
kubectl get secret talos-cluster-secrets -o jsonpath='{.data.client_key}' | base64 -d
```

### Network Configuration
Update IP addresses and endpoints in examples to match your environment:
- Control plane endpoint (e.g., `https://192.168.1.100:6443`)
- Talos API endpoint (e.g., `192.168.1.100:50000`)
- Node IP addresses

### Version Compatibility
Examples use:
- Talos: `v1.11.0`
- Kubernetes: `v1.32.1`

Update versions as needed for your deployment.

## Resource Dependencies

Resources should be created in this order:

1. **ProviderConfig** - Authentication
2. **Secrets** - Machine secrets generation
3. **FactorySchematic** - Custom image (optional)
4. **Configuration** - Machine configurations
5. **ConfigurationApply** - Apply to nodes
6. **Bootstrap** - Initialize cluster
7. **Kubeconfig** - Access cluster

## Troubleshooting

### Check Resource Status
```bash
kubectl get managed
kubectl describe secrets.machine.talos.crossplane.io example-machine-secrets
```

### Provider Logs
```bash
kubectl logs -n crossplane-system -l app=provider-talos
```

### Connection Secrets
```bash
kubectl get secrets -o wide | grep connection
```
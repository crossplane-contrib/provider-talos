# provider-talos

`provider-talos` is a [Crossplane](https://crossplane.io/) Provider for managing
[Talos Linux](https://www.talos.dev/) infrastructure. It provides comprehensive
support for Talos cluster lifecycle management including:

- **MachineSecrets** - Generate and manage machine secrets for Talos clusters
- **Configuration** - Generate Talos machine configurations for control plane and worker nodes
- **ConfigurationApply** - Apply machine configurations to Talos nodes
- **Bootstrap** - Bootstrap Talos nodes to initialize the cluster
- **ClusterKubeconfig** - Manage Kubernetes configuration access for Talos clusters
- **ImageFactorySchematic** - Create custom Talos images through the Image Factory

## Installation

Install the provider by using the following command:

```shell
up ctp provider install crossplane-contrib/provider-talos:v0.1.0
```

Alternatively, you can use declarative installation:

```yaml
cat <<EOF | kubectl apply -f -
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-talos
spec:
  package: xpkg.upbound.io/crossplane-contrib/provider-talos:v0.1.0
EOF
```

## Configuration

1. Create a ProviderConfig with your Talos cluster credentials:

```yaml
apiVersion: talos.crossplane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: default
spec:
  # Provider configuration here
```

2. Create a Secret with your Talos certificates:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: talos-creds
  namespace: crossplane-system
type: Opaque
data:
  # Base64 encoded certificates and keys
```

## Usage

See the [examples](examples/) directory for sample manifests that create Talos infrastructure resources.

## Developing

Run the following command to build and run the provider locally:

```shell
make run
```

### Building

Run the following command to build the provider:

```shell
make build
```

### Local Development

To set up a local development environment:

```shell
# Initialize build submodules
make submodules

# Create a local kind cluster and start controllers
make dev

# Run tests
make test

# Run lints and tests
make reviewable
```

### Cleanup

To clean up the local development environment:

```shell
make dev-clean
```

## Contributing

Refer to Crossplane's [CONTRIBUTING.md] file for more information on how the
Crossplane community prefers to work. The [Provider Development][provider-dev]
guide may also be of use.

[CONTRIBUTING.md]: https://github.com/crossplane/crossplane/blob/master/CONTRIBUTING.md
[provider-dev]: https://github.com/crossplane/crossplane/blob/master/contributing/guide-provider-development.md

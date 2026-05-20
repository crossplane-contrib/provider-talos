# Crossplane Provider Talos

> **Note:** This provider is a work in progress. Contributions are welcome!

`provider-talos` is a [Crossplane](https://crossplane.io/) infrastructure provider
for [Talos Linux](https://www.talos.dev/). It is a native Crossplane provider:
resource types and controllers are hand-written using crossplane-runtime, and the
controllers integrate directly with the
[Talos Go SDK](https://github.com/siderolabs/talos/tree/main/pkg/machinery) to
manage real Talos machines.

## Overview

The Talos provider enables platform teams to create and configure Talos Linux
infrastructure using Kubernetes APIs. Resource shapes and behavior are modeled on
the [siderolabs/terraform-provider-talos](https://github.com/siderolabs/terraform-provider-talos)
provider, but no code is generated from it; the Talos SDK is the runtime
dependency.

## Features

The provider includes support for these resources:

- **Machine Secrets** - Generate and manage machine secrets for Talos clusters
- **Machine Configuration** - Generate Talos machine configurations for control plane and worker nodes  
- **Configuration Apply** - Apply machine configurations to Talos nodes
- **Bootstrap** - Bootstrap Talos nodes to initialize the cluster
- **Cluster Health** - Wait for Talos cluster health before dependent operations
- **Cluster Kubeconfig** - Retrieve Kubernetes configuration from Talos clusters
- **Image Factory Schematic** - Create custom Talos images through the Image Factory

## Getting Started

### Installation

Install the provider by using the following command after installing Crossplane:

```shell
kubectl apply -f -<<EOF
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-talos
spec:
  package: ghcr.io/crossplane-contrib/provider-talos:v0.2.0
EOF
```

Notice that the provider is installed in the crossplane-system namespace alongside Crossplane.

### Configuration

Create a ProviderConfig. The provider does not require external credentials for
local resource generation (Configuration, Secrets) and reads per-resource
client configuration from the managed resource's `clientConfiguration` field for
machine API operations:

```yaml
apiVersion: talos.crossplane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: default
spec:
  credentials:
    source: None
```

For machine API resources (ConfigurationApply, Bootstrap, ClusterHealth, Kubeconfig), supply
the Talos client certificates directly on the managed resource via
`spec.forProvider.clientConfiguration.{caCertificate,clientCertificate,clientKey}`.
Use the literal value `insecure` on all three fields to target a machine in
maintenance mode.

## Usage

### Basic Example

Here's a simple example that generates machine secrets for a Talos cluster:

```yaml
apiVersion: machine.talos.crossplane.io/v1alpha1
kind: Secrets
metadata:
  name: example-secrets
spec:
  forProvider:
    talosVersion: v1.8.0
  writeConnectionSecretToRef:
    name: example-talos-secrets
    namespace: default
  providerConfigRef:
    name: default
```

Generated machine secrets and client credentials are published to the connection
secret and are not stored in the `Secrets` resource status. `Configuration`
resources that use `machineSecretsRef` read the referenced `Secrets` resource's
Kubernetes connection secret.

Additional examples can be found in the [examples](examples/) directory.

## Developing

### Building

Build the provider:

```shell
make build
```

### Running Locally

Run the provider locally:

```shell
make run
```

### Testing

Run tests:

```shell
make test
```

### Code Generation

Regenerate Crossplane managed resource scaffolding (deepcopy methods, managed
resource interface implementations, CRDs):

```shell
make generate
```

This runs `controller-gen` and `angryjet` against the hand-written types in
`apis/` and refreshes the `zz_generated.*` files plus the CRD manifests in
`package/crds/`.

## Community

Like all Crossplane projects, this provider is driven by the community. If you have questions or feedback, please reach out:

- Crossplane [Forums](https://github.com/crossplane/crossplane/discussions)
- [Crossplane Slack](https://slack.crossplane.io/)
- [Twitter](https://twitter.com/crossplane_io)
- [Email](mailto:info@crossplane.io)

## Contributing

provider-talos is a community driven project and we welcome contributions. See the Crossplane [Contributing](https://github.com/crossplane/crossplane/blob/master/CONTRIBUTING.md) guidelines to get started.

## Report a Bug

For filing bugs, suggesting improvements, or requesting new features, please open an [issue](https://github.com/crossplane-contrib/provider-talos/issues).

## License

provider-talos is under the Apache 2.0 license.

# Crossplane Provider Talos

> **Note:** This provider is a work in progress. Contributions are welcome!

`provider-talos` is a [Crossplane](https://crossplane.io/) infrastructure provider
for [Talos Linux](https://www.talos.dev/). Built with [Upjet](https://github.com/upbound/upjet),
it exposes XRM-conformant managed resources for the Talos API.

## Overview

The Talos provider enables platform teams to create and configure Talos Linux
infrastructure using Kubernetes APIs. This provider leverages the official
[siderolabs/terraform-provider-talos](https://github.com/siderolabs/terraform-provider-talos)
to offer comprehensive Talos cluster lifecycle management.

## Features

The provider includes support for these resources:

- **Machine Secrets** - Generate and manage machine secrets for Talos clusters
- **Machine Configuration** - Generate Talos machine configurations for control plane and worker nodes  
- **Configuration Apply** - Apply machine configurations to Talos nodes
- **Bootstrap** - Bootstrap Talos nodes to initialize the cluster
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
  package: xpkg.upbound.io/crossplane-contrib/provider-talos:v0.1.0
EOF
```

Notice that the provider is installed in the crossplane-system namespace alongside Crossplane.

### Configuration

Create a ProviderConfig with your Talos cluster connection details:

```yaml
apiVersion: talos.crossplane.io/v1beta1
kind: ProviderConfig
metadata:
  name: default
spec:
  # Connection details for the Talos cluster
  configuration:
    source: Secret
    secretRef:
      namespace: crossplane-system
      name: talos-credentials
      key: credentials
```

Create a Secret containing your Talos client configuration:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: talos-credentials
  namespace: crossplane-system
type: Opaque
stringData:
  credentials: |
    context: mycluster
    contexts:
      mycluster:
        endpoints:
          - 192.168.1.100
        ca: LS0tLS1CRUdJTi0t...
        crt: LS0tLS1CRUdJTi0t...
        key: LS0tLS1CRUdJTi0t...
```

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
  providerConfigRef:
    name: default
```

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

Generate code from the Terraform provider schema:

```shell
make generate
```

This will update generated code in the `apis/` and `internal/` directories.

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

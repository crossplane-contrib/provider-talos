# Local Reproduction

This document records the local smoke test performed for the `ClusterHealth` managed resource implementation.

## Environment

- Repository: `crossplane-contrib/provider-talos`
- Commit tested: `b4f1403 Add ClusterHealth managed resource`
- Provider run mode: out-of-cluster via `go run cmd/provider/main.go --debug --poll=5s`
- Cluster: isolated kind cluster named `provider-talos-smoke`
- Container runtime note: local Docker was rootless and kind failed with `Delegate=yes` requirements, so the smoke test used the running Podman machine with `KIND_EXPERIMENTAL_PROVIDER=podman`.

## Summary of Steps

1. Created an isolated kind cluster:

   ```sh
   KIND_EXPERIMENTAL_PROVIDER=podman kind create cluster --name provider-talos-smoke
   KIND_EXPERIMENTAL_PROVIDER=podman kind get kubeconfig --name provider-talos-smoke > /tmp/provider-talos-smoke.kubeconfig
   ```

2. Installed generated CRDs from this checkout:

   ```sh
   kubectl --context kind-provider-talos-smoke apply -R -f package/crds
   kubectl --context kind-provider-talos-smoke wait --for=condition=Established crd/configurations.machine.talos.crossplane.io --timeout=60s
   kubectl --context kind-provider-talos-smoke wait --for=condition=Established crd/secrets.machine.talos.crossplane.io --timeout=60s
   kubectl --context kind-provider-talos-smoke wait --for=condition=Established crd/clusterhealths.cluster.talos.crossplane.io --timeout=60s
   ```

3. Ran the provider locally against the kind cluster:

   ```sh
   nohup env KUBECONFIG=/tmp/provider-talos-smoke.kubeconfig go run cmd/provider/main.go --debug --poll=5s > /tmp/provider-talos-smoke.log 2>&1 &
   ```

4. Applied smoke-test resources:
   - `ProviderConfig/default`
   - `Secrets/smoke-machine-secrets`
   - `Configuration/smoke-worker-config`
   - `ClusterHealth/smoke-cluster-health`

5. Verified `Secrets` and `Configuration` became ready, their connection details were generated, and status hash matched the connection secret contents.

6. Verified `ClusterHealth` controller registered and reconciled. The test intentionally used an unreachable Talos endpoint (`127.0.0.1:1`), so the resource correctly reported `Synced=True`, `Ready=False`, node count status, and a non-terminal health message while continuing to reconcile.

7. Cleaned up the provider process and kind cluster.

## CRD Installation Output

```text
customresourcedefinition.apiextensions.k8s.io/clusterhealths.cluster.talos.crossplane.io created
customresourcedefinition.apiextensions.k8s.io/kubeconfigs.cluster.talos.crossplane.io created
customresourcedefinition.apiextensions.k8s.io/factoryschematics.image.talos.crossplane.io created
customresourcedefinition.apiextensions.k8s.io/bootstraps.machine.talos.crossplane.io created
customresourcedefinition.apiextensions.k8s.io/configurationapplies.machine.talos.crossplane.io created
customresourcedefinition.apiextensions.k8s.io/configurations.machine.talos.crossplane.io created
customresourcedefinition.apiextensions.k8s.io/secrets.machine.talos.crossplane.io created
customresourcedefinition.apiextensions.k8s.io/providerconfigs.talos.crossplane.io created
customresourcedefinition.apiextensions.k8s.io/providerconfigusages.talos.crossplane.io created
customresourcedefinition.apiextensions.k8s.io/storeconfigs.talos.crossplane.io created
customresourcedefinition.apiextensions.k8s.io/configurations.machine.talos.crossplane.io condition met
customresourcedefinition.apiextensions.k8s.io/secrets.machine.talos.crossplane.io condition met
customresourcedefinition.apiextensions.k8s.io/clusterhealths.cluster.talos.crossplane.io condition met
```

## Provider Startup Log Showing ClusterHealth Registration

```text
2026-05-20T14:51:20-04:00 INFO  Starting EventSource {"controller": "managed/clusterhealth.cluster.talos.crossplane.io", "controllerGroup": "cluster.talos.crossplane.io", "controllerKind": "ClusterHealth", "source": "kind source: *v1alpha1.ClusterHealth"}
2026-05-20T14:51:20-04:00 INFO  Starting Controller  {"controller": "managed/clusterhealth.cluster.talos.crossplane.io", "controllerGroup": "cluster.talos.crossplane.io", "controllerKind": "ClusterHealth"}
2026-05-20T14:51:20-04:00 INFO  Starting workers     {"controller": "managed/clusterhealth.cluster.talos.crossplane.io", "controllerGroup": "cluster.talos.crossplane.io", "controllerKind": "ClusterHealth", "worker count": 10}
```

## Base Smoke Resource Readiness

```text
providerconfig.talos.crossplane.io/default created
secrets.machine.talos.crossplane.io/smoke-machine-secrets created
configuration.machine.talos.crossplane.io/smoke-worker-config created
secrets.machine.talos.crossplane.io/smoke-machine-secrets condition met
configuration.machine.talos.crossplane.io/smoke-worker-config condition met
```

## Connection Detail Verification

Decoded connection detail sizes:

```text
    2708 /tmp/smoke-machine-config.yaml
    8828 /tmp/smoke-machine-secrets.json
   11536 total
```

Expected generated and patched fields were present:

```text
    type: worker
        environment: kind-smoke
        endpoint: https://10.0.0.1:6443
    clusterName: smoke-cluster
```

Connection secret bytes matched the status hash and the compatibility status field:

```text
hashes match: 72b2b150295e793a40f48f910c10f799c37baa4c07a624b4a944430c11570584
status matches secret: 72b2b150295e793a40f48f910c10f799c37baa4c07a624b4a944430c11570584
True
72b2b150295e793a40f48f910c10f799c37baa4c07a624b4a944430c11570584
2026-05-20T18:51:45Z
```

## ClusterHealth Reconciliation Output

The smoke test applied a `ClusterHealth` resource with valid generated Talos client credentials and an intentionally unreachable endpoint (`127.0.0.1:1`). This verifies the new controller is registered, observes, records useful status, marks the resource unavailable while converging/unreachable, and does not turn the normal not-ready health check into a terminal sync failure.

Status output:

```text
True
False

1
waiting for cluster health: rpc error: code = Unavailable desc = connection error: desc = "transport: Error while dialing: dial tcp 127.0.0.1:1: connect: connection refused"
```

The fields above correspond to:

1. `Synced=True`
2. `Ready=False`
3. `status.atProvider.healthy` omitted/false
4. `status.atProvider.checkedControlPlaneNodes=1`
5. `status.atProvider.lastMessage` showing the bounded health check result

Relevant controller log output:

```text
2026-05-20T14:52:02-04:00 DEBUG provider-talos Reconciling {"controller": "managed/clusterhealth.cluster.talos.crossplane.io", "request": {"name":"smoke-cluster-health"}}
2026-05-20T14:52:02-04:00 DEBUG provider-talos Successfully requested update of external resource {"controller": "managed/clusterhealth.cluster.talos.crossplane.io", "request": {"name":"smoke-cluster-health"}, "external-name": "", "requeue-after": "2026-05-20T14:52:07-04:00"}
2026-05-20T14:52:07-04:00 DEBUG provider-talos Reconciling {"controller": "managed/clusterhealth.cluster.talos.crossplane.io", "request": {"name":"smoke-cluster-health"}}
2026-05-20T14:52:07-04:00 DEBUG provider-talos Successfully requested update of external resource {"controller": "managed/clusterhealth.cluster.talos.crossplane.io", "request": {"name":"smoke-cluster-health"}, "external-name": "smoke-cluster-health", "requeue-after": "2026-05-20T14:52:12-04:00"}
2026-05-20T14:52:12-04:00 DEBUG provider-talos Reconciling {"controller": "managed/clusterhealth.cluster.talos.crossplane.io", "request": {"name":"smoke-cluster-health"}}
2026-05-20T14:52:12-04:00 DEBUG provider-talos Successfully requested update of external resource {"controller": "managed/clusterhealth.cluster.talos.crossplane.io", "request": {"name":"smoke-cluster-health"}, "external-name": "smoke-cluster-health", "requeue-after": "2026-05-20T14:52:17-04:00"}
```

## Cleanup

```sh
kill "$(cat /tmp/provider-talos-smoke.pid)"
KIND_EXPERIMENTAL_PROVIDER=podman kind delete cluster --name provider-talos-smoke
```

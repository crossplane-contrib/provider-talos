# Basic Talos Provider Demo

This demonstrates the working Talos Crossplane provider with minimal setup.

## Quick Test

```bash
# Apply the basic demo resources
kubectl apply -f examples/basic-demo/

# Check status (should show SYNCED=True for both)
kubectl get secrets.machine.talos.crossplane.io,configurations.machine.talos.crossplane.io

# Watch detailed status
watch kubectl get secrets.machine.talos.crossplane.io,configurations.machine.talos.crossplane.io \
  -o custom-columns="NAME:.metadata.name,KIND:.kind,READY:.status.conditions[?(@.type==\"Ready\")].status,SYNCED:.status.conditions[?(@.type==\"Synced\")].status"
```

## Expected Results

Both resources should show `SYNCED=True`:
- **Secrets**: Generates Talos machine secrets
- **Configuration**: Generates Talos machine configuration  

## Clean Up

```bash
kubectl delete -f examples/basic-demo/
```

This basic demo proves the provider is working correctly without needing actual Talos machines.
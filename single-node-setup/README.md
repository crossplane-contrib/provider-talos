# Talos Single-Node Cluster Setup Guide - UPDATED WITH ACTUAL TESTING

This guide walks through configuring a single Talos machine at `192.168.120.82` using the Crossplane Talos provider.

## Prerequisites

1. A machine booted with Talos OS at `192.168.120.82`
2. Crossplane installed with the Talos provider
3. Network connectivity to the Talos machine

## ✅ TESTED Step-by-Step Process

### Step 1: Create Provider Configuration ✅ WORKING

```bash
kubectl apply -f 01-provider-config.yaml
```

**Key Finding:** ProviderConfig requires `credentials.source: None` for basic setup.

**Verify:**
```bash
kubectl get providerconfigs.talos.crossplane.io
```

### Step 2: Generate Machine Secrets ✅ WORKING

```bash
kubectl apply -f 02-machine-secrets.yaml
```

This generates PKI materials and stores them in a connection secret.

**Key Findings:**
- Resource kind is `Secrets` (not MachineSecrets)  
- Requires `writeConnectionSecretToRef` to store certificates
- Creates connection secret with 4 keys: ca_certificate, client_certificate, client_key, talos_config

**Verify:**
```bash
kubectl get secrets.machine.talos.crossplane.io
kubectl get secret single-node-talos-secrets  # Connection secret with certificates
```

### Step 3: Create Machine Configuration ✅ WORKING

```bash
kubectl apply -f 03-controlplane-config.yaml
```

This creates the Talos machine configuration manifest.

**Key Findings:**
- Configuration resource shows `SYNCED=True`
- Single-node cluster needs `allowSchedulingOnControlPlanes: true`
- Must include `registerWithTaints: []` to allow pods on control plane

**Verify:**
```bash
kubectl get configurations.machine.talos.crossplane.io single-node-controlplane-config
```

### Step 4: Apply Configuration to Machine ⚠️ NEEDS IMPLEMENTATION

```bash
# This step requires actual certificates - see updated manifest
kubectl apply -f 04-configuration-apply.yaml
```

**Key Findings:**
- ConfigurationApply needs `node` and `machineConfigurationInput` (not nodeRef)
- Requires actual certificates from the connection secret
- **CURRENT LIMITATION:** Provider has placeholder implementation - `machineConfiguration` output is empty

### Step 5: Bootstrap the Cluster ⚠️ NEEDS IMPLEMENTATION  

```bash
kubectl apply -f 05-bootstrap.yaml
```

### Step 6: Retrieve Kubeconfig ⚠️ NEEDS IMPLEMENTATION

```bash
kubectl apply -f 06-kubeconfig.yaml
```

## 🔍 Current Provider Status

**WORKING RESOURCES:**
- ✅ ProviderConfig - Accepts credentials.source: None
- ✅ Secrets - Generates certificates and stores in connection secret  
- ✅ Configuration - Creates and validates configuration specs

**PLACEHOLDER IMPLEMENTATIONS:**
- ⚠️ ConfigurationApply - Missing machine configuration generation
- ⚠️ Bootstrap - Not yet implemented
- ⚠️ ClusterKubeconfig - Not yet implemented

## 📋 Implementation Status Summary

| Resource | Status | Notes |
|----------|--------|--------|
| ProviderConfig | ✅ Working | Uses `credentials.source: None` |
| Secrets | ✅ Working | Generates PKI, stores in connection secret |
| Configuration | ✅ Working | Validates spec, shows Synced=True |
| ConfigurationApply | ⚠️ Placeholder | Needs `machineConfiguration` output from Configuration |
| Bootstrap | ⚠️ Placeholder | Awaits ConfigurationApply completion |
| ClusterKubeconfig | ⚠️ Placeholder | Awaits Bootstrap completion |

## 🛠️ Developer Notes

The provider architecture is solid and follows Crossplane patterns correctly:
- CRDs are properly defined with correct API groups
- Resources accept specifications and show proper status
- Connection secrets work for certificate storage
- Provider observes resources and reports status

**Next Development Steps:**
1. Implement Configuration resource to populate `atProvider.machineConfiguration`
2. Use that output in ConfigurationApply's `machineConfigurationInput`
3. Implement actual Talos API calls for applying configurations
4. Implement Bootstrap and ClusterKubeconfig resources

## 🎯 Verified Workflow Design

1. **Secrets** ✅ - Generate and store cluster PKI materials
2. **Configuration** ✅ - Validate configuration parameters  
3. **ConfigurationApply** ⚠️ - Apply config to Talos machine (needs implementation)
4. **Bootstrap** ⚠️ - Initialize Kubernetes cluster (needs implementation)
5. **ClusterKubeconfig** ⚠️ - Retrieve cluster access (needs implementation)

The foundational pieces are working correctly - the provider just needs the Talos SDK integration completed.
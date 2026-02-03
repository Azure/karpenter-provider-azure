# Proactive Scale-Up Implementation

## Overview

This implementation adds true proactive scale-up to Azure Karpenter by modifying Karpenter core locally to support pod injection hooks. Fake pods are injected into Karpenter's internal provisioning logic (in memory only) based on workload definitions, triggering node provisioning before real pods become pending.

## Architecture

### Modified Karpenter Core

Located in `vendor-local/karpenter/`, this is a modified copy of Karpenter v1.7.1 with the following additions:

**1. PodInjector Interface** (`pkg/controllers/provisioning/podinjector.go`)
```go
type PodInjector interface {
    InjectPods(ctx context.Context, realPods []*corev1.Pod) []*corev1.Pod
}
```

**2. Provisioner Modifications** (`pkg/controllers/provisioning/provisioner.go`)
- Added `podInjector` field to Provisioner struct
- Modified `GetPendingPods()` to call `podInjector.InjectPods()`
- Added `SetPodInjector()` method to configure custom injector
- Default no-op injector for backward compatibility

**3. Controllers Signature** (`pkg/controllers/controllers.go`)
- Modified `NewControllers()` to return `([]controller.Controller, *Provisioner)`
- Allows Azure provider to access and configure the provisioner

### Azure Provider Implementation

**Pod Injector** (`pkg/controllers/proactivescaleup/injector.go`)

Implements the `PodInjector` interface:
1. Monitors Deployments, ReplicaSets, StatefulSets, Jobs
2. Calculates gap: `desired replicas - actual pods`
3. Generates fake Pod objects in memory for the gap
4. Fake pods have:
   - Same resource requests as real pods
   - Same scheduling constraints (nodeSelector, affinity, tolerations)
   - Labels for identification (`karpenter.azure.com/fake-pod: "true"`)
   - Low priority (-1000)
   - Pending status

**Integration** (`cmd/controller/main.go`)
- Creates pod injector when feature is enabled
- Sets injector on provisioner via `SetPodInjector()`
- Logs "Proactive scale-up enabled with pod injection"

## How It Works

```
┌─────────────────────────────────────────────────────┐
│ 1. User scales Deployment from 5 to 10 replicas    │
└─────────────────────────────────────────────────────┘
                        ↓
┌─────────────────────────────────────────────────────┐
│ 2. Injector detects gap: 10 desired - 5 actual = 5 │
└─────────────────────────────────────────────────────┘
                        ↓
┌─────────────────────────────────────────────────────┐
│ 3. Injector creates 5 fake Pod objects IN MEMORY   │
│    (never sent to Kubernetes API)                  │
└─────────────────────────────────────────────────────┘
                        ↓
┌─────────────────────────────────────────────────────┐
│ 4. Karpenter's GetPendingPods() returns:           │
│    [5 real pending pods] + [5 fake pods] = 10 pods │
└─────────────────────────────────────────────────────┘
                        ↓
┌─────────────────────────────────────────────────────┐
│ 5. Karpenter scheduler sees 10 pending pods        │
│    Provisions nodes for all 10                     │
└─────────────────────────────────────────────────────┘
                        ↓
┌─────────────────────────────────────────────────────┐
│ 6. Nodes become ready                              │
└─────────────────────────────────────────────────────┘
                        ↓
┌─────────────────────────────────────────────────────┐
│ 7. Deployment controller creates 5 new real pods   │
│    They schedule immediately on new nodes          │
└─────────────────────────────────────────────────────┘
                        ↓
┌─────────────────────────────────────────────────────┐
│ 8. Next GetPendingPods() call:                     │
│    Gap is now 0, no fake pods injected             │
└─────────────────────────────────────────────────────┘
```

## Key Benefits

✅ **In-Memory Only**: Fake pods never created via Kubernetes API
✅ **No kubectl Visibility**: Fake pods don't appear in `kubectl get pods`
✅ **Zero API Server Overhead**: No additional objects created
✅ **Automatic**: Based on workload definitions, not pending pods
✅ **Transparent**: Real pods unaware of fake pods
✅ **Clean**: No cleanup needed, fake pods exist only during provisioning cycle

## Configuration

### Enable Feature

```bash
# Via flag
--enable-proactive-scaleup=true

# Via environment variable
ENABLE_PROACTIVE_SCALEUP=true
```

### Set Limits

```bash
--pod-injection-limit=5000  # Max total pods (real + fake)
--node-limit=15000          # Max total nodes
```

## Local Karpenter Core

The `go.mod` file contains:
```go
replace sigs.k8s.io/karpenter => ./vendor-local/karpenter
```

This tells Go to use the local modified version instead of the upstream version.

**Note**: The `vendor-local/karpenter` directory contains the modified Karpenter core v1.7.1 and is committed to the repository. When you clone this branch, the modified core is included.

### Updating Karpenter Core

If you need to update to a newer Karpenter version:

1. Copy the new version to `vendor-local/karpenter`:
   ```bash
   cp -r $(go env GOMODCACHE)/sigs.k8s.io/karpenter@v1.x.x vendor-local/karpenter
   chmod -R u+w vendor-local/karpenter
   ```

2. Apply the modifications:
   - Add `podinjector.go`
   - Modify `provisioner.go`
   - Modify `controllers.go`

3. Test:
   ```bash
   go build ./cmd/controller/...
   ```

## Comparison with Alternatives

### This Approach (Local Core Modification)
✅ Fake pods only in memory
✅ No Kubernetes API objects
✅ Clean implementation
⚠️ Requires maintaining local core copy

### Creating Real Pods Approach
✅ Works with unmodified core
✅ Simple implementation
❌ Creates real Kubernetes objects
❌ API server overhead
❌ Visible in kubectl

### Forking Core Repository
✅ Fake pods only in memory
❌ High maintenance burden
❌ Complex to keep in sync
❌ Diverges from upstream

## Testing

### Build
```bash
go build ./cmd/controller/...
```

### Run Locally
```bash
# With feature enabled
ENABLE_PROACTIVE_SCALEUP=true \
POD_INJECTION_LIMIT=5000 \
NODE_LIMIT=15000 \
./controller
```

### Verify
```bash
# Scale a deployment
kubectl scale deployment/my-app --replicas=20

# Check Karpenter logs
kubectl logs -n karpenter deployment/karpenter -f | grep "proactive\|fake-pod"

# Verify fake pods aren't created
kubectl get pods -A | grep fake-pod  # Should return nothing

# Watch nodes being provisioned
kubectl get nodes -w
```

## Future Improvements

If Karpenter core adds official pod injection support in the future:
1. Remove local modifications
2. Update to use official hooks
3. Remove `go mod replace` directive
4. Continue using the same injector implementation

The `Injector` implementation in Azure provider can remain unchanged since it implements the same interface.

## References

- [Kubernetes Autoscaler PR #7145](https://github.com/kubernetes/autoscaler/pull/7145) - Original proactive scale-up inspiration
- [Karpenter Documentation](https://karpenter.sh/) - Official Karpenter docs

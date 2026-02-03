# Proactive Scale-Up for Azure Karpenter

## Challenge and Architecture Constraints

### The Goal
Implement proactive scale-up similar to Kubernetes Cluster Autoscaler PR #7145, where fake pods are injected into the provisioning logic to trigger node creation BEFORE real pods become pending.

### The Challenge with Karpenter

Unlike Cluster Autoscaler which has a `PodListProcessor` that can modify pod lists before processing, Karpenter's architecture makes this difficult:

1. **Core Provisioning Logic**: The provisioner is in `sigs.k8s.io/karpenter` (core), not in the Azure provider
2. **Internal Method Calls**: `GetPendingPods()` is called internally by `Schedule()`, both in the core Provisioner
3. **No Extension Points**: Karpenter core doesn't provide hooks to inject fake pods into the provisioning logic

### Architecture Comparison

**Cluster Autoscaler (PR #7145)**:
```
GetPods() → PodListProcessor.Process() → [realPods + fakePods] → Autoscaler
                    ↑ Injection point
```

**Karpenter**:
```
Provisioner.Schedule() → [internal] GetPendingPods() → k8s API → Scheduler
                              ↑ No external access
```

### Possible Solutions

#### Option 1: Create Real Fake Pods (Current Implementation)
**Status**: ✅ Implemented

**Approach**:
- Controller monitors Deployments, ReplicaSets, StatefulSets, Jobs
- Creates actual Pod objects in Kubernetes for the gap
- Labels them as fake pods for identification
- Karpenter sees them as pending and provisions nodes
- Auto-cleanup when not needed

**Pros**:
- Works with Karpenter's existing architecture
- No modifications to core needed
- Clean separation of concerns

**Cons**:
- Creates actual Pod objects (visible in `kubectl get pods`)
- Slight API server overhead

**Usage**:
```bash
# Enable feature
--enable-proactive-scaleup=true

# Pods will be created automatically based on workload gaps
kubectl get pods -l karpenter.azure.com/fake-pod=true
```

#### Option 2: Fork Core Provisioner
**Status**: ❌ Not Recommended

**Approach**:
- Copy Karpenter core provisioner code to Azure provider
- Modify `GetPendingPods()` to inject fake pods
- Maintain forked version

**Pros**:
- Fake pods only in memory

**Cons**:
- High maintenance burden (keep in sync with upstream)
- Code duplication
- Fragile across Karpenter versions

#### Option 3: Custom Informer Cache
**Status**: ⚠️ Complex and Fragile

**Approach**:
- Wrap pod informer cache
- Intercept List() calls to add fake pods
- Requires deep integration with controller-runtime

**Pros**:
- Fake pods only in memory
- No pod objects created

**Cons**:
- Very complex implementation
- Fragile across controller-runtime versions
- May break other controllers
- Hard to maintain

#### Option 4: Wait for Core Support
**Status**: 🔮 Future

**Approach**:
- Propose changes to Karpenter core
- Add pod injection hooks
- Implement in Azure provider once available

**Pros**:
- Clean, maintainable solution
- Fake pods only in memory
- Supported by core team

**Cons**:
- Requires upstream changes
- Timeline uncertain

## Recommendation

**Use Option 1 (Current Implementation)** because:

1. **It works** with Karpenter's architecture
2. **Minimal overhead** - fake pods are lightweight (pause container, low priority)
3. **Automatic management** - controller handles lifecycle
4. **Clean separation** - labeled and annotated for identification
5. **Maintainable** - doesn't rely on Karpenter internals

The fake pods being actual Kubernetes objects is a minor trade-off for a working, maintainable solution that achieves the goal: **proactive node provisioning before real workload pods appear**.

## How It Works

```
1. Deployment scaled: 5 → 10 replicas
2. Controller detects gap: 5 pods needed
3. Controller creates 5 fake pods (actual K8s objects)
4. Karpenter sees 5 pending pods
5. Karpenter provisions nodes
6. Real pods created by Deployment
7. Real pods schedule on new nodes
8. Fake pods deleted automatically
```

## Configuration

```bash
# Enable proactive scale-up
--enable-proactive-scaleup=true

# Set limits
--pod-injection-limit=5000
--node-limit=15000

# Or via environment variables
ENABLE_PROACTIVE_SCALEUP=true
POD_INJECTION_LIMIT=5000
NODE_LIMIT=15000
```

## Monitoring

```bash
# View fake pods
kubectl get pods -A -l karpenter.azure.com/fake-pod=true

# Check controller logs
kubectl logs -n karpenter -l app.kubernetes.io/name=karpenter --tail=100 | grep proactivescaleup
```

## Future Improvements

If Karpenter core adds support for pod list processors or injection hooks in the future, this implementation can be updated to inject fake pods only in memory without creating actual Pod objects.

## References

- [Kubernetes Autoscaler PR #7145](https://github.com/kubernetes/autoscaler/pull/7145)
- [Karpenter Architecture](https://karpenter.sh/docs/concepts/)

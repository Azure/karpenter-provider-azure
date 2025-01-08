# Cleaning Up Orphaned Network Interfaces

## Motivation and Goals
When using the Azure provider, we create five Azure resources when a NodeClaim is launched:

1. **Network Interface (NIC)**  
2. **Virtual Machine (VM)**  
3. **VM AKS Identifying Extension**  
4. **VM CSE Extension**  
5. **Optionally, a Disk** (if we are not using ephemeral disks)

When we attempt a launch, if that launch fails, we cache the instance type as unavailable and retry. In this retry, we can reuse the same network interface until its `RegistrationTTL` has expired. After the `RegistrationTTL` period, the NodeClaim lifecycle controller (the `liveness` controller) removes the NodeClaim and all associated resources.

However, sometimes the deletion of Azure resources fails with the error `NicReservedForAnotherVM`. Azure requires a waiting period of 180 seconds before deleting the network interface in such scenarios.

We currently have a situation where, in cases of `NicReservedForAnotherVM`, some NICs that fail to be deleted are never garbage-collected. We do not want to block the NodeClaim reconciler from finalizing deletion of network interfaces. Therefore, we need an asynchronous garbage collection mechanism that:

1. Persists through controller restarts.
2. Can handle NICs that have failed deletion attempts in the past.

---

## Proposed Approaches for Garbage Collecting Network Interfaces
Currently, we only attempt to delete network interfaces when their corresponding NodeClaim is deleted. A NodeClaim can be deleted for various reasons, including ICE errors, consolidation, other disruption behaviors, or NodeClaim registration failures.

### Approach A: "Hijack" the Existing `List()` Method
We can add the orphaned network interfaces to the list of NodeClaims returned by our `CloudProvider.List()` method. This way, our existing controller logic will detect these interfaces as “instances” without corresponding NodeClaims in Kubernetes, and remove them—assuming they are beyond the registration TTL.

**Pros:**
- Does not require changes to the core Karpenter code.
- Does not introduce any new dependencies to the garbage collection controller.
- Maintains parity with the other cloud providers.

**Cons:**
- `List()` is conceptually a list of instances, not arbitrary garbage-collection candidates. If the core code ever uses `List()` for something else, this approach might create conflicts or require future re-engineering.

### Approach B: Move NodeClaim GC Controllers to Core and Rename `List()` to `RemovableOrphans()`
A concept of garbage-collecting resources exists across cloud providers in an almost identical fashion for AKS, EKS, and Alibaba Cloud. We could:

1. Move all garbage collection controllers into the core Karpenter code.
2. Introduce a new `CloudProvider` method (e.g., `RemovableOrphans()`) for specifying resources that need garbage collection.

Azure primarily needs to garbage-collect two resource types:
1. **Network Interfaces** (NICs)
2. **Virtual Machines**

Deletion for both is normally handled by `NodeClaim.Delete()`. We could modify the current `List()` call to get the necessary behavior for removing orphaned network interfaces.  

Meanwhile, AWS, Azure, and Alibaba Cloud all use their respective `List()` methods in essentially the same way. They only use it in the garbage collection controllers, all of which have a similar structure:

- [AWS Implementation](https://github.com/aws/karpenter-provider-aws/blob/main/pkg/controllers/nodeclaim/garbagecollection/controller.go)  
- [Azure Implementation](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/controllers/nodeclaim/garbagecollection/controller.go)  
- [Alibaba Implementation](https://github.com/cloudpilot-ai/karpenter-provider-alibabacloud/blob/main/pkg/controllers/nodeclaim/garbagecollection/controller.go)

We could rename `List()` to `RemovableOrphans()` to be more semantically correct and then consolidate the garbage collection controllers in the core, so all providers share the same logic.

### Approach C: Amend the GC Controller to Have a New Process: `garbageCollectOrphanedNics()`
We could introduce custom logic specifically in the Azure garbage collection controller:

```go
func (c *Controller) garbageCollectOrphanedNICs(ctx context.Context) error {
    // List all network interfaces managed by Karpenter
    nics, err := c.ListKarpenterNics(ctx)
    if err != nil {
        return fmt.Errorf("listing network interfaces, %w", err)
    }

    // Get all NodeClaims to check against
    nodeClaimList := &karpv1.NodeClaimList{}
    if err = c.kubeClient.List(ctx, nodeClaimList); err != nil {
        return err
    }

    // Create a set of NodeClaim names for efficient lookup
    nodeClaimNames := sets.New[string]()
    for _, nc := range nodeClaimList.Items {
        nodeClaimNames.Insert(nc.Name)
    }

    // Check each NIC and delete if orphaned
    for _, nic := range nics {
        // Extract the NodeClaim name from the NIC name (e.g., "aks-{nodeclaimname}")
        nicName := *nic.Name
        if !strings.HasPrefix(nicName, "aks-") {
            continue
        }
        nodeClaimName := strings.TrimPrefix(nicName, "aks-")

        // If the NodeClaim doesn't exist, this NIC is orphaned
        if !nodeClaimNames.Has(nodeClaimName) {
            if err := c.cloudProvider.Delete(ctx, nodeClaimName); err != nil {
                logging.FromContext(ctx).With("nic-name", nicName).Errorf("failed to delete orphaned network interface: %v", err)
                continue
            }
            logging.FromContext(ctx).With("nic-name", nicName).Info("deleted orphaned network interface")
        }
    }

    return nil
}

func (c *Controller) Reconcile(ctx context.Context) (reconcile.Result, error) {
    ctx = injection.WithControllerName(ctx, "instance.garbagecollection")

    // First handle VM garbage collection
    retrieved, err := c.cloudProvider.List(ctx)
    if err != nil {
        return reconcile.Result{}, fmt.Errorf("listing cloudprovider VMs, %w", err)
    }
    managedRetrieved := lo.Filter(retrieved, func(nc *karpv1.NodeClaim, _ int) bool {
        return nc.DeletionTimestamp.IsZero()
    })

    nodeClaimList := &karpv1.NodeClaimList{}
    if err = c.kubeClient.List(ctx, nodeClaimList); err != nil {
        return reconcile.Result{}, err
    }
    nodeList := &v1.NodeList{}
    if err := c.kubeClient.List(ctx, nodeList); err != nil {
        return reconcile.Result{}, err
    }

    resolvedProviderIDs := sets.New[string](lo.FilterMap(nodeClaimList.Items, func(n karpv1.NodeClaim, _ int) (string, bool) {
        return n.Status.ProviderID, n.Status.ProviderID != ""
    })...)

    errs := make([]error, len(retrieved))
    workqueue.ParallelizeUntil(ctx, 100, len(managedRetrieved), func(i int) {
        if !resolvedProviderIDs.Has(managedRetrieved[i].Status.ProviderID) &&
           time.Since(managedRetrieved[i].CreationTimestamp.Time) > time.Minute*5 {
            errs[i] = c.garbageCollect(ctx, managedRetrieved[i], nodeList)
        }
    })
    if err = multierr.Combine(errs...); err != nil {
        return reconcile.Result{}, err
    }

    // After VM garbage collection, attempt to clean up any orphaned NICs
    if err := c.garbageCollectOrphanedNICs(ctx); err != nil {
        return reconcile.Result{}, fmt.Errorf("garbage collecting orphaned NICs: %w", err)
    }

    c.successfulCount++
    return reconcile.Result{
        RequeueAfter: lo.Ternary(c.successfulCount <= 20, time.Second*10, time.Minute*2),
    }, nil
}
```

Pros:
	•	Does not require changes to the core Karpenter code.
	•	Keeps List() usage focused on listing VMs.

Cons:
	•	Adds custom logic in the GC controller specific to Azure.

--- 

### Deletion via CloudProvider.Delete()
All these approaches assume that CloudProvider.Delete() can be used to remove network interfaces.
Currently, Delete() calls:
```go
func (c *CloudProvider) Delete(ctx context.Context, nodeClaim *karpv1.NodeClaim) error {
    ctx = logging.WithLogger(ctx, logging.FromContext(ctx).With("nodeclaim", nodeClaim.Name))

    vmName, err := utils.GetVMName(nodeClaim.Status.ProviderID)
    if err != nil {
        return fmt.Errorf("getting VM name, %w", err)
    }
    return c.instanceProvider.Delete(ctx, vmName)
}

// In DefaultProvider
func (p *DefaultProvider) Delete(ctx context.Context, resourceName string) error {
    logging.FromContext(ctx).Debugf("Deleting virtual machine %s and associated resources", resourceName)
    return p.cleanupAzureResources(ctx, resourceName)
}

func (p *DefaultProvider) cleanupAzureResources(ctx context.Context, resourceName string) error {
    vmErr := deleteVirtualMachineIfExists(ctx, p.azClient.virtualMachinesClient, p.resourceGroup, resourceName)
    if vmErr != nil {
        logging.FromContext(ctx).Errorf("virtualMachine.Delete for %s failed: %v", resourceName, vmErr)
    }
    // The order here is intentional:
    //  1. Delete the VM if it exists (also deletes NIC, Disk, and associated resources).
    //  2. If the VM did not exist, attempt to delete the NIC directly.
    nicErr := deleteNicIfExists(ctx, p.azClient.networkInterfacesClient, p.resourceGroup, resourceName)
    if nicErr != nil {
        logging.FromContext(ctx).Errorf("networkInterface.Delete for %s failed: %v", resourceName, nicErr)
    }

    return errors.Join(vmErr, nicErr)
}
```
The current logic obtains the VM name from ProviderID, which is not populated if the Node never successfully launches. Therefore, for garbage collecting VMs that never launched, we should modify CloudProvider.Delete() to support name lookup from the NodeClaim name rather than the provider ID.

## Methods for Listing Network Interfaces 

### Approach A: Azure Resource Graph (ARG) List
```go
func GetNICListQueryBuilder(rg string) *kql.Builder {
    return kql.New(`Resources`).
        AddLiteral(` | where type == "microsoft.network/networkinterfaces"`).
        AddLiteral(` | where resourceGroup == `).AddString(strings.ToLower(rg)).
        AddLiteral(` | where tags["`).AddString(NodePoolTagKey).AddLiteral(`"] == "`).AddString("karpenter").AddLiteral(`"`)
}
```
We can use the ARG Resources api to query for network interfaces belonging to karpenter, and if a network interface doesn't have a nodeclaim associated with it, we simply remove it.

ARG Provides a convenient list api, at the cost of some of the data lagging by a factor of minutes. This is ok since
Pros:
	•	Reduced API call overhead (one ARG call instead of multiple).
	•	Good enough for garbage collection since real-time data is not critical.

Cons:
	•	ARG data can be slightly delayed compared to Network Resource Provider (NRP). Meaning some network interfaces may not be garbage collected right away. For small subnets this may be problematic. 
	•	May not reflect the most immediate changes, which is generally fine for garbage collection.


### Approach B: Network Resource Provider (NRP) List
We can use the azure sdk for go's ListPager, and iterate through all of the network interfaces


Pros:
	•	Provides fresh data straight from the NRP control plane
Cons:
	•	Potentially more expensive calls; could consume NIC read quotas if listing a large number of NICs.



---
### Additional Considerations for NicReservedForAnotherVM Errors
	1.	Introducing a --force-delete: Ideally, Azure’s network resource provider might offer a --force-delete option for NICs. However, such a feature would require changes in the Azure API, which is outside our control.
	2.	Reducing Quota Errors: We can leverage Azure quota APIs to filter out instance types that exceed our quotas before creating VMs, reducing orphaned NICs in the first place. This could remove the latency and overhead of provisioning attempts that inevitably fail.
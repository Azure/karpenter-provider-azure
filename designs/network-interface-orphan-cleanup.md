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
3. Doesn't lead to nrp, crp or arm throttling.
---

## Proposed Approaches for Garbage Collecting Network Interfaces
Currently, we only attempt to delete network interfaces when their corresponding NodeClaim is deleted. A NodeClaim can be deleted for various reasons, including ICE errors, consolidation, other disruption behaviors, or NodeClaim registration failures.

### Approach A: Single Garbage collection controller
This approach recommends a single controller with a single reconcilation for deleting any orphan network interfaces, and any orphan virtual machines in each Singleton Reconcilation.

We should share the virtual machines list and network interface list, then find deletion candidates. With a list of resourceNames to remove we could treat them in 2 ways.

1. Treat all resources as instanceprovider delete calls regardless of type, after all the instanceProvider.Delete method will attempt deletion of all types of resources we need to gc.
2. Resource aware deletion, that attempts deletion for each individual resource and has retries and reonciles configured for each one

### Approach B: Two Controllers Independent of each other
We can have a virtual machines controller, and a network interfaces controller.

The virtual machines controller and network interface controller will be responsible for retrieving all of their own state, and garbage collecting their namesake. We will populate a cache in the network interfaces controller that will mark network interfaces as unremovable if they meet the following 3 criteria
1. Reserved by NRP: When creating a nic, and attempting to assign it to a vm(linked via arm resource id), the nic will be reserved for that resource_id for 180 seconds
2. Belongs to a nodeclaim on the cluster: If the nodeclaim exists for a nic, we do not want to attempt its removal
3. Belongs to a VM: If the VM Garbage controller can remove it, we should not be attempting to remove the nic in this controller. We should instead delegate that responsibility to the vm gc controller since deleting a successfully provisioned vm has delete options to also celan up the associated nics, and any other attached resources(vm extensions for example)

We also want to avoid removing nics that are not managed by karpenter, but our ListNics query will handle that responsibility for us.

Q: Should we batch network interface deletion?
- https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/request-limits-and-throttling#network-throttling
NRP Sets limits of 1000 deletions or creations per 5 minutes, and 10,000 reads per 5 minutes
A: For simplicity, we will not be batching deletions and rely on the throttling from NRP. The reconciler will continue to retry every 5 minutes and eventually clean up all of the network interfaces

### Approach C: Two separate controllers with ResourceLister
Alternatively we could implement a ResourceLister that can share resource lists between the virtualmachines gc controller and the networkinterfaces gc controller.

Network interfaces + VirtualMachines controllers share both the NodeClaim.List() state, and cloudprovider.List state.

We can implement a ResourceLister that is shared between the two that will cache the results of the individual list calls. We can use this resource lister in other places as well like the InstanceProvider, updating the resources on successful creation, and ocassionally adding in new entries via arg list.
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
We solved the high level ideas for the controllers, and decided on an approach for how we will attempt removal, and share states. Now lets move onto how we will retrieve the correct network interfaces.

### Design for InstanceProvider.ListNics(ctx context.Context) error {}
### Approach A: Azure Resource Graph (ARG) List
This approach uses the Azure Resource Graph (ARG) API to query for network interfaces belonging to Karpenter. If a network interface does not have an associated NodeClaim, it is identified as orphaned and removed. ARG provides a convenient and efficient way to list resources, although its data may lag by a few minutes.

**Example Query:**
```go
func GetNICListQueryBuilder(rg string) *kql.Builder {
    return kql.New(`Resources`).
        AddLiteral(` | where type == "microsoft.network/networkinterfaces"`).
        AddLiteral(` | where resourceGroup == `).AddString(strings.ToLower(rg)).
        AddLiteral(` | where tags["`).AddString(NodePoolTagKey).AddLiteral(`"] == "`).AddString("karpenter").AddLiteral(`"`)
}
```


Q: Should we List both NIC + VMs together in the arg query for the network interface controller?
**Pros:**
- Reduced API call overhead (one ARG query instead of multiple API calls).
- Sufficient for garbage collection since real-time data is not critical.

**Cons:**
- ARG data may lag compared to the Network Resource Provider (NRP), causing delays in garbage collection.
- For small subnets, delays in NIC cleanup could lead to resource exhaustion.
- Clusters with a high number of NICs (e.g., 80k+) may face performance issues or even out-of-memory (OOM) errors with a single query.

### Approach B: Network Resource Provider (NRP) List
This approach uses the Azure Network Resource Provider (NRP) API via the ListPager method to iterate through all network interfaces. This provides real-time data but comes at the cost of higher API usage and potential quota limitations.

**Example Query**
```go
func ListNetworkInterfaces(ctx context.Context, client *armnetwork.InterfacesClient) ([]armnetwork.Interface, error) {
    var allNics []armnetwork.Interface
    pager := client.ListAll(ctx, nil)
    for pager.More() {
        page, err := pager.NextPage(ctx)
        if err != nil {
            return nil, fmt.Errorf("listing network interfaces: %w", err)
        }
        allNics = append(allNics, page.Value...)
    }
    return allNics, nil
}
```

**Pros:**
- Provides fresh, real-time data directly from the NRP control plane.
- Ensures immediate garbage collection of orphaned NICs.
**Cons:**
- Increased latency if the cluster has a large number of nics, arg caches reads whereas we would be getting these live
- higher api call overhead compared to arg, since we are paginating
- may consume NIC Read quota, causing other parts of karpenter to fail if reached

### Additional Consideration: Modifying the List Method to take in a cloudprovider ResourceType
Alternatively, we could modify the cloudprovider interface List to have lists for different cloudprovider resources(Nic, Instance, Disk).

We can either use the existing interface and implement the abscraction ourselves, or modify the interface to have a ResourceType parameter. Lets opt for not modifying core.

We still would need to modify the gc controllers, but we could share the same interface for List, not requiring any importing of the InstanceProvider
```go
// List returns nodeclaims associated with a particular ResourceType
func (c *CloudProvider) List(ctx context.Context) ([]*karpv1.NodeClaim, error) {

        switch options.FromContext(ctx).ResourceType {
                case "instance":
                instances, err := c.instanceProvider.List(ctx)
                if err != nil {
                        return nil, fmt.Errorf("listing instances, %w", err)
                }

                var nodeClaims []*karpv1.NodeClaim
                for _, instance := range instances {
                        instanceType, err := c.resolveInstanceTypeFromInstance(ctx, instance)
                        if err != nil {
                                return nil, fmt.Errorf("resolving instance type, %w", err)
                        }
                        nodeClaim, err := c.instanceToNodeClaim(ctx, instance, instanceType)
                        if err != nil {
                                return nil, fmt.Errorf("converting instance to node claim, %w", err)
                        }

                        nodeClaims = append(nodeClaims, nodeClaim)
                }
                return nodeClaims, nil
                case "networkinterfaces":
                nics,err := c.instanceProvider.ListNics(ctx)
                if err != nil {
                        return nil,fmt.Errorf("listing nics, %w", err)
                }
                nc, err := c.nicToNodeClaim(ctx, nic)
                if err != nil {
                return fmt.Errorf("converting nic to nodeclaim, %w", err)
                }
        }
}


```

**Pros:**
- Provides a flexible framework for handling different resource types
- Could be extended to handle other resource types in the future if needed(probably not)
- Maintains single point of entry through List() for any generic cloudprovider resource List
- Allows for resource-specific handling while keeping common interface
- allows for custom lists to be defined, for example we can define a list of all nics + vms that need to be gc'd have have the list return that having a switch case for it
- Doesn't require us to import the InstanceProvider to list cloudprovider resources in our garbage collection controller, we could easily abstract the same logic in the gc controller to have the changes there be minimal. Simply calling the same gc code twice for two resource types.
**Cons:**
- Adds complexity to the List() interface
- Could make error handling more complex


---
### Additional Considerations for reducing NicReservedForAnotherVM Errors
        1.      Introducing a --force-delete: Ideally, Azure‚Äôs network resource provider might offer a --force-delete option for NICs. However, such a feature would require changes in the Azure API, which is outside our control.
        2.      Reducing Quota Errors: We can leverage Azure quota APIs to filter out instance types that exceed our quotas before creating VMs, reducing orphaned NICs in the first place. This could remove the latency and overhead of provisioning attempts that inevitably fail.


### Musings or ideas that don't work for dealbreaking reasons
These ideas don't work because they are impure, just leaving here for reference
### Approach A: "Hijack" the Existing `List()` Method
We can add the orphaned network interfaces to the list of NodeClaims returned by our `CloudProvider.List()` method. This way, our existing controller logic will detect these interfaces as ‚Äúinstances‚Äù without corresponding NodeClaims in Kubernetes, and remove them‚Äîassuming they are beyond the registration TTL.

**Pros:**
- Simplest Implementation as it just leverages existing GC Controller implementation with a minor List call addition to a part of the code that already has access to it
- Maintains parity with the other cloud providers for the GC controller
- Reuses existing reconciliation logic and timing mechanisms, including workqueue parallelization

**Cons:**
- `List()` is conceptually a list of instances, not arbitrary garbage-collection candidates. If the core code ever uses `List()` for something else, this approach might create conflicts or require future re-engineering.
- Semantically incorrect - List() should return instances, not arbitrary resources
- Could cause issues if core Karpenter code starts using List() for other purposes
- Potentially harder to debug as NICs and VMs are treated the same way
- Harder to implement deletion ordering, nics should only be attempted to be gc'd after vms. Doable via having them appeneded to the List after vms are appended

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

**Pros:**
- Easier to extend for future resource types (as shown in the Azure implementation needing both NICs and VMs)
**Cons:**
- Requires significant changes to core Karpenter code
- All cloud providers would need to update their implementations
- More complex implementation initially
- Requires coordination across multiple repositories and organizations


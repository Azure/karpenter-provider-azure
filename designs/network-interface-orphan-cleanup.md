# Cleaning Up Orphaned Network Interfaces

The document is organized into the following sections:


0. **Introduction** Why do we need this work in the first place? 
1. **Proposed Solutions for Orphaned Network Interfaces**: Presents and evaluates four approaches to solving the problem:
   - Extending the existing `List()` method to include orphaned NICs.
   - Centralizing garbage collection logic in the core Karpenter codebase and introducing a new method for identifying removable orphaned resources.
   - Adding a new garbage collection process (`garbageCollectOrphanedNICs`) specifically tailored for Azure NICs.
   - Modifying the `List()` method to support resource-specific listings for flexible garbage collection.

2. **Handling Deletion of Network Interfaces**: Details the current deletion logic implemented in `CloudProvider.Delete()` and proposes enhancements to support scenarios where NodeClaims lack a populated `ProviderID`.

3. **Network Interface Listing Methods**: Explores two approaches to retrieving lists of orphaned NICs:
   - Using Azure Resource Graph (ARG) for efficient queries at the expense of slightly delayed data.
   - Using the Network Resource Provider (NRP) for real-time, up-to-date data at the cost of increased API calls.

4. **Additional Considerations**: Discusses complementary strategies for mitigating orphan NIC issues, including:
   - Introducing a `--force-delete` option for NICs in Azure.
   - Using quota APIs to prevent provisioning failures and reduce orphaned resources.

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

Pros:
- Most semantically correct approach
- Provides clear separation between instance listing and orphaned resource collection which is what List is used for primarily
- Allows for standardized garbage collection across all cloud providers, and utilizes the abstraction of cloudprovider.Delete to cleanup resources neatly
- Makes the codebase more maintainable long-term, moving shared code to be shared
- Easier to extend for future resource types (as shown in the Azure implementation needing both NICs and VMs) 
Cons:
- Requires significant changes to core Karpenter code
- All cloud providers would need to update their implementations
- More complex implementation initially
- Requires coordination across multiple repositories and organizations

### Approach C: Amend the GC Controller to Have a New Process: `garbageCollectOrphanedNics()` and bring in the instance provider for listing nics

We could Extend the InstanceProvider to List Network Interfaces and call a new gc method for orphaned nics. This requires we bring in the instance provider 
into the gc controller. This makes the most sense since our clients for accessing instances and their related resources all live here.
```go    
nics, err := c.InstanceProvider.ListNics(ctx)
```

```go
func (c *Controller) Reconcile(ctx context.Context) (reconcile.Result, error) {
    ctx = injection.WithControllerName(ctx, "instance.garbagecollection")

    // First handle VM garbage collection
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
- Clear separation of concerns between VM and NIC garbage collection
- Keeps existing List() functionality pure
- Can leverage existing instance provider code and clients
- More flexibility in the cleanup of nics in things such as reconcilation requeue(Could requeue 180 seconds if cloudprovider delete fails with nicReservedForAnotherVM) 

Cons:
- Azure-specific solution in the garbage collection controller straying from shared behavior between all gc controllers in all providers 
- Duplicates some GC logic 


### Approach D: Modifying the List Method to take in a cloudprovider ResourceType 
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

Pros: 
- Provides a flexible framework for handling different resource types
- Could be extended to handle other resource types in the future if needed(probably not)
- Maintains single point of entry through List() for any generic cloudprovider resource List
- Allows for resource-specific handling while keeping common interface
- allows for custom lists to be defined, for example we can define a list of all nics + vms that need to be gc'd have have the list return that having a switch case for it 
- Doesn't require us to import the InstanceProvider to list cloudprovider resources in our garbage collection controller, we could easily abstract the same logic in the gc controller to have the changes there be minimal. Simply calling the same gc code twice for two resource types.
Cons:
- Adds complexity to the List() interface
- Could make error handling more complex


## Recommendation
We recommend Approach C for the following reasons:
1. It provides a clean separation of concerns while requiring no changes to the core Karpenter codebase
2. It leverages existing instance provider functionality
3. It allows for Azure-specific handling of the NicReservedForAnotherVM error case and it can be implemented without affecting other cloud providers
4. It provides a clear separation of concerns for Network Interface Listing. By doing a second nodeclaim List after gc of the vms, we cleanly only attempt deletion of network interfaces not associated with vms.
While Approach B (moving to core) might be the cleanest long-term solution, Approach C provides the best balance of implementation complexity and immediate problem-solving for the Azure provider's specific needs.
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
Pros:
- Reduced API call overhead (one ARG query instead of multiple API calls).
- Sufficient for garbage collection since real-time data is not critical.

Cons:
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

Pros:
- Provides fresh, real-time data directly from the NRP control plane.
- Ensures immediate garbage collection of orphaned NICs.
Cons:
- Increased latency if the cluster has a large number of nics, arg caches reads whereas we would be getting these live
- higher api call overhead compared to arg, since we are paginating 
- may consume NIC Read quota, causing other parts of karpenter to fail if reached 

---
### Additional Considerations for reducing NicReservedForAnotherVM Errors
	1.	Introducing a --force-delete: Ideally, Azure’s network resource provider might offer a --force-delete option for NICs. However, such a feature would require changes in the Azure API, which is outside our control.
	2.	Reducing Quota Errors: We can leverage Azure quota APIs to filter out instance types that exceed our quotas before creating VMs, reducing orphaned NICs in the first place. This could remove the latency and overhead of provisioning attempts that inevitably fail.

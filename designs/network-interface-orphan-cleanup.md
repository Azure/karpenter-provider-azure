# Overview 
In the azure provider, we create 5 azure resources on nodeclaim launch 

1. NetworkInterface 
2. VirtualMachine
3. VirtualMachine AKS Identifying Extension
4. VirtualMachine CSE Extensionnn 
5. Optionally a Disk if we are not using Ephemeral Disks 

We will attempt a launch, and if that launch fails, we will cache an instance type as unavailable and retry. In the retry we can reuse that same network interface until after the `RegistrationTTL` is reached. After the registrationTTL is reached, the nodeclaim lifecycle controller `liveness` will remove the nodeclaim and child resources. Sometimes this deletion on the azure side will fail, with an error `NicReservedForAnotherVM`, and before you can delete that network interface, you must wait 180 seconds.  

We have a problem where in cases of NicReservedForAnotherVM, we are not garbage collecting orphan nics that fail deletion. We don't want to block the nodeclaim reconciler from finalizing deletion of network interfaces, and thus need some sort of async garbage collection. This solution must persist through controller restart, and be able to collect nics that have failed deletion on other attempts. 

## Approaches: Garbage Collect Network Interfaces 
We attempt to delete network interfaces right now only on nodeclaim deletion. Nodeclaim deletion can come from ICE errors, consolidation + other disruption behaviors, or nodeclaim registration failure. 

## Approach A: Simply Hijack List without changing core 
We can simply add the network interfaces we want to garbage collect to our CloudProvider.List() method and that will solve the garbage collection for us with our current controller. 
If there are karpenter managed nics existing when a karpenter node doesn't exist for that nic, and its beyond registration ttl, we want to remove those network interfaces. By returning 
Nodeclaims in the List() call that represent the network interfaces, we won't find a node for the orphan nics, and they will be removed.

### Pros + Cons 
+ Requires minimal changes to core 
- List() method assumes its a list of instances, not gc candidates. If core decides to use List() for something else, we may be in a situation where we can't upgrade to a new version of core until we reengineer a solution for network interface GC.
## Approach B: Move nodeclaim gc controllers to core and rename List() to RemovableOrphans()
This concept of garbage collecting a resource exists between cloudproviders in the exact same way for AKS, EKS, and AlibabaCloud. We could move all of the garbage collection controllers into core. Then have a new CloudProvider method that explicitly is for specifying garbage collection candidates.

Azure needs to garbage collect 2 resources primarily 
1. NetworkInterfaces(NIC, Disk + VMExtension deletion is associated with VM Deletion, but nic is a special case because we always create a nic before we successfully create a vm.) 
2. VirtualMachines

deletion of both resources are covered by a Nodeclaim.Delete() call. We can modify the existing List() call to get the behavioral change needed to remove orphan network interfaces

AWS, Azure, and Alibaba's CloudProvider all have identical usage of the List() method, and its only used in the garbage collection controllers which all have the exact same shape.

See same exact GC Shape in all three:
[AWS](https://github.com/aws/karpenter-provider-aws/blob/main/pkg/controllers/nodeclaim/garbagecollection/controller.go)
[Azure](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/controllers/nodeclaim/garbagecollection/controller.go)
[Alibaba](https://github.com/cloudpilot-ai/karpenter-provider-alibabacloud/blob/main/pkg/controllers/nodeclaim/garbagecollection/controller.go)

All three providers use List() in the exact same way. See the code search query on cloudprovider.List() usage [here](https://github.com/search?q=%28+++repo%3AAzure%2Fkarpenter-provider-azure+OR++++repo%3AAWS%2Fkarpenter-provider-aws+OR++++repo%3Acloudpilot-ai%2Fkarpenter-provider-alibabacloud+OR++++repo%3Acloudpilot-ai%2Fkarpenter-provider-gcp+OR++++repo%3Akubernetes-sigs%2Fkarpenter-provider-cluster-api+OR++++repo%3Akubernetes-sigs%2Fkarpenter+%29+cloudProvider.List%28ctx%29&type=code)

Both azure and aws call List() to get the candidates to remove they both get from the instanceProvider.List(). We could modify this to be a core concept where cloudproviders just return a filter of the candidates to be removed via the RemovableOrphans() method. Azure could specify the nodeclaim delete() be called again for a orphan network interface or orphan virtual machine. Effectively renaming List() to RemovableOrphans which is closer to what its purpose is today. Then move all the identical garbage collection controllers into core and have it all maintained there instead of separately.



## Approaches in Listing Network Interfaces 

### Approach A: Azure Resource Graph List

```go 
func GetNICListQueryBuilder(rg string) *kql.Builder {
	return kql.New(`Resources`).
		AddLiteral(` | where type == "microsoft.network/networkinterfaces"`).
		AddLiteral(` | where resourceGroup == `).AddString(strings.ToLower(rg)). // Ensure the resource group is lowercase
		AddLiteral(` | where tags["`).AddString(NodePoolTagKey).AddLiteral(`"] == "`).AddString("karpenter").AddLiteral(`"`)
}
```
#### Pros + Cons 
++ Reduced API call Overhead: 
-- Cons: ARG Data isn't as real time as the NRP data would be, this isn't super important when garbage collecting unused network interfaces unless the user is on a very limited address space and desperately needs the ips available in the subnet

### Approach B: Network Resource Provider List
+ Fresh Data 
-- More expensive list calls, as we will have to use ListPager to make many batched calls  
- counts toward NIC read quota 


## Honorable Mentions for solving NicReservedForAnotherVM errors
These solutions were considered for solving NicReservedForAnotherVM, but these solutions don't solve all orphan network interface cases. In the bulk of cases currently they are caused by NicReservedForAnotherVM, but we may see other cases for orphan nic gc.
Hence these didn't make the list, but are still worthy considerations in their own right.
### Introduction of a NetworkInterface ForceDelete 
Ideally the network resource provider would allow us to delete the network interfaces with a `--force-delete` flag on `/interfaces/delete` operations regardless if a NIC is associated with a vm. Most of the cases the vm its associated with ends up not existing due to a quota error, but the association happens before that and we can't reassociate. This whole problem comes from the fact we have a nic attached to virtual machine create that was never successful.

```
az network nic delete \
  --name <nicName> \
  --resource-group <rgName> \
  --force-delete
```

The problem here is that it requires work in azure on a service our team does not have ownership of in an area that is slow moving toward change. We should still raise this usecase internally to see if we can see change in this api structure so RPs like AKS don't have to design our own async gc processes around deletion of a resource we do not want ownership of anymore.


## Remove the Quota Errors that cause the leftover nics from NicReservedForAnotherVM
We could explore using the quota apis to reduce the number of nics.
This isn't really a solution as it still wouldn't solve for orphan network interfaces, but is an adj solution to greatly reduce the network interfaces we fail to remove. 
We could and should move to using reliable quota apis that we can use to filter out instance types from consideration at the InstanceTypes Provider filter level, vs the current mechanism which attempts to provision the vm, and 
when we get a quota error back from CRP, caches the sku as unavailable and we retry with a new one. 

This introduces latency into VM Creation, and adds additional CRP calls eating away at our limited quota. Its a worthy consideration for followup work.


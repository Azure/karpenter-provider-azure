# Design for BYO Subnet 


## Overview/Motivation 
The motivation here is that we have multiple customers that need to specify multiple subnets in different pools for network segregation. We want to be able to support these scenarios and to do so karpenter nodepools need to be able to specify a subnet for just the nodepool.

The api interface that makes the most sense since this is provider specific is putting SubnetID in the AKSNodeClass. Maybe there is some world where subnet info could fall into the Nodepool since mulitple cloudproviders have need for surfacing this configuration interface, but networking is inherently a cloudprovider owned surface area.




# API Design 

## Limitations 
- We will not be supporting subnet drift 
- We will not be supporting specifying mulitple vnets. 


## SubnetID vs SubnetSelectorTags 
###  Decision: SubnetID 
Easiest to just follow the existing AKS pattern here.

## Should SubnetID be immutable 
### How should immutability be enforced? 
To talk about immutability, we should first talk about validation. Validation is critical to the decision we make here on implementation 
### Validation Requirements 
1. The Shape of resource ID should be in the shape of a valid subnet id on azure 
2. The resource should exist
3. Karpenter has proper RBAC for subnet/read and subnet/join on this
4. The Subnet specified must belong to the same vnet as the one specifed on cluster create.

Any solution we come up with for validation of this field needs to be able to meet these requirements. This rules out CEL Validation exclusively. We 
can have supplemental validation done with cel on the resource ID shape and content, but we need to have outbound api calls to validate the resource ID exists
and that we have proper RBAC

#### Option A: Validation -- via Admission Webhooks 
We previously made the decision to remove the admission webhooks from karpenter 


#### Option B: Validation -- via Runtime Validation and Status Conditions 
We can perform runtime validation, and only set the status of a field as immutable once the validation

## Should SubnetID be required? 

### Yes case + behavior 
### No case + behavior 

## AKS Defaults 
Should the default nodeclass specified in node auto provisioning, and the default nodeclass


## SubnetID specified per AKSNodeClass 



### Q: Should it be immutable 


#### Q: How should immutability be enforced? 
1. Webhooks come back 
2. CEL Validation 
3. Annotation and runtime validation 
### Q: Should it be required
If we don't require subnetID to be specified on the nodeclass, we could fallback to the subnetID specified on the managed cluster. The problem 
with this method means that if one were to specify a subnetID later in the lifecycle of CRD, we would have to migrate the nodes to the new subnetID 
specified on the nodeclass.
### Q: How does subnet drift work? 


## Cluster Level VNETSubnetID 
Karpenter currently allows users to specify a vnet subnet id on the initial cluster create. This is the subnet karpenter will fallback to in t
- https://github.com/Azure/karpenter-provider-azure/pull/238

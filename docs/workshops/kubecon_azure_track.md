Table of contents:
- [Overview](#overview)
- [Basic Cheet Sheet](#basic-cheet-sheet)
- [Main Topics](#main-topics)
    - [Step: Install Karpenter](#step-install-karpenter)
    - [Step: Basic NodePool](#step-basic-nodepool)
        - [Step: Scaling Application](#step-scaling-application)
        - [Step: Limit Resources](#step-limit-resources)
        - [Step: Disruption](#step-disruption)
        - [Step: Drift](#step-drift)
        - [Step: RightSizing](#step-rightsizing)
    - [Step: Consolidation](#step-consolidation)
        - [Step: Single Node Consolidation](#step-single-node-consolidation)
        - [Step: Multi Node Consolidation](#step-multi-node-consolidation)
- [Bonus Content (optional)](#bonus-content-optional)
    - [Step: Scheduling Constraints](#step-scheduling-constraints)
    - [Step: Disruption Control](#step-disruption-control)
- [Cleanup](#cleanup)

## Overview

This document details the steps to follow the KubeCon workshop using an AKS cluster in Azure.

To follow along using this workshop, simply go through the steps detailed in this document. For each step of the workshop, since Karpenter is built in the open source, and has a lot of cloud agnostic elements, there is a link to the AWS version (in the step's header), as much of the AWS workshop will translate directly over to Azure. However, for any steps where it diviates, there will be a few important notes, and adjustments detailed under those steps.

## Basic Cheet Sheet

When you see `eks-node-viewer` use `aks-node-viewer` instead.

> Note: if you ever end up needing to use the extended log command to look back over a longer period of time, make sure its using the `kube-system` namespace like follows:
> ```bash
> kubectl -n kube-system logs -f deployment/karpenter --all-containers=true --since=20m
> ```

## Main Topics

### Step: [Install Karpenter](https://github.com/Azure/karpenter-provider-azure/tree/main/docs/workshops/1_aks_cluster_creation_and_install_karpenter.md)


### Step: [Basic NodePool](https://github.com/Azure/karpenter-provider-azure/blob/main/docs/workshops/2_basic_nodepool.md)

- Notes:
    - AKSNodeClass is Azure’s equivalence to EC2NodeClass for Azure specific settings. Each Karpenter NodePool must contain a reference to an AKSNodeClass via the spec.template.spec.nodeClassRef.

### Step: [Scaling Application](https://catalog.workshops.aws/karpenter/en-US/basic-nodepool/scaling)

- Notes:
    - The creation of the node and nodeclaim by karpenter might take ~90s. However, you can confirm the node claims creation in the `k8s` api beforehand using the command:
        ```bash
        kubectl get nodeclaims
        ```

### Step: [Limit Resources](https://catalog.workshops.aws/karpenter/en-US/basic-nodepool/limit)

> Concepts translate to Azure.

### Step: [Disruption](https://catalog.workshops.aws/karpenter/en-US/basic-nodepool/ttlsecondsafterempty)

> Concepts translate to Azure.

### Step: [Drift](https://catalog.workshops.aws/karpenter/en-US/basic-nodepool/drift)

- Instead follow [6_drift.md](https://github.com/Azure/karpenter-provider-azure/tree/main/docs/workshops/6_drift.md)

### Step: [RightSizing](https://catalog.workshops.aws/karpenter/en-US/basic-nodepool/rightsizing)

- Adjustments:
    - Just use `aks-node-viewer` instead of `eks-node-viewer`. However, there are some important notes below on understanding the AKS Karpetner logs, since they differ slightly from the AWS ones, as the instance types being considered are different.

- Notes:
    - In the logs below you can see it considered the following set of instance types for the requests of `"cpu":"7350m","memory":"7738Mi","pods":"13"},"instance-types":"Standard_D13_v2, Standard_D4_v2, Standard_D8_v3, Standard_D8_v4, Standard_D8_v5 and 14 other(s)"}`.
    - It then chose `Standard_D8_v3`, and `Standard_D2_v3` as the final two instance types.
    - The following karpenter log output came from the command `kubectl -n $KARPENTER_NAMESPACE logs -l app.kubernetes.io/name=karpenter`. (note: a few logs have been replaced with `...` for focus and simplicity.)
        ```
        {"level":"INFO","time":"2024-11-08T00:02:15.039Z","logger":"controller","message":"found provisionable pod(s)","commit":"d83a94c","controller":"provisioner","namespace":"","name":"","reconcileID":"68c92eeb-f714-4ac6-9653-7b1862722022","Pods":"workshop/inflate-759cbbb648-ws6pt, workshop/inflate-759cbbb648-56nlh, workshop/inflate-759cbbb648-f2lgq, workshop/inflate-759cbbb648-ltp2t, workshop/inflate-759cbbb648-x7kql and 3 other(s)","duration":"14.389408ms"}
        {"level":"INFO","time":"2024-11-08T00:02:15.039Z","logger":"controller","message":"computed new nodeclaim(s) to fit pod(s)","commit":"d83a94c","controller":"provisioner","namespace":"","name":"","reconcileID":"68c92eeb-f714-4ac6-9653-7b1862722022","nodeclaims":2,"pods":8}
        {"level":"INFO","time":"2024-11-08T00:02:15.048Z","logger":"controller","message":"created nodeclaim","commit":"d83a94c","controller":"provisioner","namespace":"","name":"","reconcileID":"68c92eeb-f714-4ac6-9653-7b1862722022","NodePool":{"name":"default"},"NodeClaim":{"name":"default-pbfc5"},"requests":{"cpu":"7350m","memory":"7738Mi","pods":"13"},"instance-types":"Standard_D13_v2, Standard_D4_v2, Standard_D8_v3, Standard_D8_v4, Standard_D8_v5 and 14 other(s)"}
        {"level":"INFO","time":"2024-11-08T00:02:15.048Z","logger":"controller","message":"created nodeclaim","commit":"d83a94c","controller":"provisioner","namespace":"","name":"","reconcileID":"68c92eeb-f714-4ac6-9653-7b1862722022","NodePool":{"name":"default"},"NodeClaim":{"name":"default-m9h2r"},"requests":{"cpu":"1350m","memory":"1594Mi","pods":"7"},"instance-types":"Standard_D11_v2, Standard_D2_v2, Standard_D2_v3, Standard_D2_v4, Standard_D2_v5 and 14 other(s)"}
        {"level":"info","ts":1731024135.0687907,"logger":"fallback","caller":"instance/instance.go:611","msg":"Selected instance type Standard_D8_v3"}
        ...
        {"level":"info","ts":1731024136.088467,"logger":"fallback","caller":"instance/instance.go:611","msg":"Selected instance type Standard_D2_v3"}
        ...
        {"level":"INFO","time":"2024-11-08T00:03:39.061Z","logger":"controller","message":"launched nodeclaim", ... "instance-type":"Standard_D8_v3","zone":"","capacity-type":"on-demand","allocatable":{"cpu":"7820m","ephemeral-storage":"128G","memory":"27174266470","pods":"110"}}
        ...
        {"level":"INFO","time":"2024-11-08T00:03:39.686Z","logger":"controller","message":"launched nodeclaim", ... "instance-type":"Standard_D2_v3","zone":"","capacity-type":"on-demand","allocatable":{"cpu":"1900m","ephemeral-storage":"128G","memory":"5226731929","pods":"110"}}
        ```

### Step: [Consolidation](https://catalog.workshops.aws/karpenter/en-US/cost-optimization/consolidation)

- Notes:
    - Spot instances work slightly different within Azure, and Spot-to-Spot Consolidation is not currently supported.

### Step: [Single Node Consolidation](https://catalog.workshops.aws/karpenter/en-US/cost-optimization/consolidation/single-node)

- Adjustments:
    - In initial cleanup, replace the command to cleanup the `ec2nodeclass`, with:
        > Note: it might pause for a few seconds on this command
        ```bash
        kubectl delete aksnodeclass default
        ```
    - The same concepts within the workshop generally translate to AKS. However, for the deployment step of the NodePool, use a new deployment command with consolidation enabled. Found in [9_single_node_consolidation.md](https://github.com/Azure/karpenter-provider-azure/tree/main/docs/workshops/9_single_node_consolidation.md)

### Step: [Multi Node Consolidation](https://catalog.workshops.aws/karpenter/en-US/cost-optimization/consolidation/multi-node)

- Notes:
    - The consolidation might take a solid amount of time, especially when going down to only 2 nodes.

- Adjustments:
    - In initial cleanup, replace the command to cleanup the `ec2nodeclass`, with:
        > Note: it might pause for a few seconds on this command
        ```bash
        kubectl delete aksnodeclass default
        ```
    - The same concepts within the workshop generally translate to AKS, but with different instances/pricing. However, for the deployment step of the NodePool, use a new deployment command with consolidation enabled. Found in [10_multi_node_consolidation.md](https://github.com/Azure/karpenter-provider-azure/tree/main/docs/workshops/10_multi_node_consolidation.md)

## Bonus Content (optional)

Everything beyond this point is optional. Although, if skipping these steps, you will still want to [Cleanup](#cleanup) your resources.

### Step: [Scheduling Constraints](https://catalog.workshops.aws/karpenter/en-US/scheduling-constraints#how-does-it-work)

> Concepts translate to Azure.

### Step: [NodePool Disruption Budgets](https://catalog.workshops.aws/karpenter/en-US/scheduling-constraints/nodepool-disruption-budgets)

- Adjustments:
    - In initial cleanup, replace the command to cleanup the `ec2nodeclass`, with:
        > Note: it might pause for a few seconds on this command
        ```bash
        kubectl delete aksnodeclass default
        ```
    - The same concepts within the workshop generally translate to AKS. However, for the 3 NodePool deployment commands, use the replacement deployment commands listed in [12_scheduling_constraints.md](https://github.com/Azure/karpenter-provider-azure/tree/main/docs/workshops/12_scheduling_constraints.md)

### Step: [Disruption Control](https://catalog.workshops.aws/karpenter/en-US/scheduling-constraints/disable-eviction)

- Adjustments:
    - In initial cleanup, replace the command to cleanup the `ec2nodeclass`, with:
        > Note: it might pause for a few seconds on this command
        ```bash
        kubectl delete aksnodeclass default
        ```
        > Note: it's expected to see an error for the inflate-pdb cleanup, and this can be ignored.
    - The same concepts within the workshop generally translate to AKS. However, for the deployment step of the NodePool, use the deployment command found in [13_disruption_controls.md](https://github.com/Azure/karpenter-provider-azure/tree/main/docs/workshops/13_disruption_controls.md)
    - > Note: don't be surprised if after the `expireAfter` of `2m` has occurred that there are new instances being created, and removed. This is expected.
    - > Note: you may see a log for selecting the instance type and resolving the image after nodeclaim creation.
    - > Note: `triggering termination for expired node after TTL`, and `deprovisioning via expiration` are not actually expected to show up within the logs.

## Cleanup

Once you've completed the workshop, ensure you cleanup all the resources to prevent any additional costs.

> Note: if you've had any disconnects from the Cloud Shell, ensure your subscription is set
> ```bash
> env | grep AZURE_SUBSCRIPTION_ID
> ```
> If you see no output from the above command, than re-select your subscription to use (replace `<personal-azure-sub>` with your azure subscription guid):
>
> ```bash
> export AZURE_SUBSCRIPTION_ID=<personal-azure-sub>
> az account set --subscription ${AZURE_SUBSCRIPTION_ID}
> ```

To cleanup all the azure resources, simply delete the resource group:

> Confirm `y` to deleting the resource group, and proceeding with the operation.

> Note: this will take a couple minutes

```bash
az group delete --name ${RG}
```

The Cloud Shell should automatically clean itself up. However, if you want to pre-emptively remove all the files we created within the workshop, simply delete them with the following command:

```bash
cd ~/
rm -rf ~/environment
```

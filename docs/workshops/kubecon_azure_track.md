Table of contents:
- [Overview](#overview)
- [Basic Cheet Sheet](#basic-cheet-sheet)
- [Adjustments](#adjustments)
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

## Overview

This document details the steps to follow the KubeCon workshop using an AKS cluster in Azure.

To follow along using this workshop, simply go through the steps detailed in this document. For each step of the workshop, since Karpenter is built in the open source, and has a lot of cloud agnostic elements, there is a link to the AWS version (in the step's header), as much of the AWS workshop will translate directly over to Azure. However, for any steps where it diviates, there will be a few important notes, and adjustments detailed under those steps.

## Basic Cheet Sheet

When you see `aws-node-viewer` use `aks-node-viewer` instead.

## Adjusted Instructions

### Step: [Install Karpenter](https://catalog.workshops.aws/karpenter/en-US/install-karpenter)

- Instead follow [1_install_karpenter.md](https://github.com/Azure/karpenter-provider-azure/tree/main/docs/workshops/1_install_karpenter.md)

### Step: [Basic NodePool](https://catalog.workshops.aws/karpenter/en-US/basic-nodepool)

- Notes:
    - AKSNodeClass is Azure’s equivalence to EC2NodeClass for Azure specific settings. Each Karpenter NodePool must contain a reference to an AKSNodeClass via the spec.template.spec.nodeClassRef.

- Adjustments:
    - The same concepts within the workshop generally translate to AKS. However, for the actual deployment step, we need a `AKSNodeClass`, and a few additional Azure specific adjustments. So, instead of the given deployment command follow [2_basic_noodpool.md](https://github.com/Azure/karpenter-provider-azure/tree/main/docs/workshops/2_basic_noodpool.md) 

### Step: [Scaling Application](https://catalog.workshops.aws/karpenter/en-US/basic-nodepool/scaling)

- Notes:
    - The creation of the node and nodeclaim by karpenter might take ~90s. However, you can confirm the node claims creation in the `k8s` api beforehand using the command:
        ```bash
        kubectl get nodeclaims -A
        ```

- Adjustments:
    - Just use `aks-node-viewer` instead of `aws-node-viewer`.

### Step: [Limit Resources](https://catalog.workshops.aws/karpenter/en-US/basic-nodepool/limit)

- Adjustments:
    - Just use `aks-node-viewer` instead of `aws-node-viewer`.

### Step: [Disruption](https://catalog.workshops.aws/karpenter/en-US/basic-nodepool/ttlsecondsafterempty)

- Adjustments:
    - Just use `aks-node-viewer` instead of `aws-node-viewer`.

### Step: [Drift](https://catalog.workshops.aws/karpenter/en-US/basic-nodepool/drift)

- Instead follow [6_drift.md](https://github.com/Azure/karpenter-provider-azure/tree/main/docs/workshops/6_drift.md)

### Step: [RightSizing](https://catalog.workshops.aws/karpenter/en-US/basic-nodepool/rightsizing)

- Adjustments:
    - Just use `aks-node-viewer` instead of `aws-node-viewer`. However, there are some important notes below on understanding the AKS Karpetner logs, since they differ slightly from the AWS ones, as the instance types being considered are different.

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

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

This document highlights all the required adjustments within the kubecon workshop for using an AKS cluster in Azure.

## Basic Cheet Sheet

When you see one of these terms replace it with the following:
- `aws-node-viewer` -> `aks-node-viewer`
- `eks-immersion-team: my-team` -> `aks-workshop: karpenter`
    - Note: just `eks-immersion-team` -> `aks-workshop`
- `public.ecr.aws/eks-distro/kubernetes/pause:3.7` -> `mcr.microsoft.com/oss/kubernetes/pause:3.6`

## Adjusted Instructions

### Step: [Install Karpenter](https://catalog.workshops.aws/karpenter/en-US/install-karpenter)

- Instead follow [1_install_karpenter.md](https://github.com/Azure/karpenter-provider-azure/tree/main/docs/workshops/1_install_karpenter.md)

### Step: [Basic NodePool](https://catalog.workshops.aws/karpenter/en-US/basic-nodepool)

- Notes:
    - AKSNodeClass is Azure’s equivalence to EC2NodeClass for Azure specific settings. Each Karpenter NodePool must contain a reference to an AKSNodeClass via the spec.template.spec.nodeClassRef.

- Adjustments:
    - The same concepts within the workshop generally translate to AKS. However, for the actual deployment step, we need a `AKSNodeClass`, and a few additional Azure specific adjustmetns. So, instead of the given deployment command follow [2_basic_noodpool.md](https://github.com/Azure/karpenter-provider-azure/tree/main/docs/workshops/2_basic_noodpool.md) 

### Step: [Scaling Application](https://catalog.workshops.aws/karpenter/en-US/basic-nodepool/scaling)

- Adjustments:
    - Only requires [Basic Cheet Sheet](#basic-cheet-sheet)

### Step: [Limit Resources](https://catalog.workshops.aws/karpenter/en-US/basic-nodepool/limit)

- Adjustments:
    - Only requires [Basic Cheet Sheet](#basic-cheet-sheet)

### Step: [Disruption](https://catalog.workshops.aws/karpenter/en-US/basic-nodepool/ttlsecondsafterempty)

- Adjustments:
    - Only requires [Basic Cheet Sheet](#basic-cheet-sheet)

### Step: [Drift](https://catalog.workshops.aws/karpenter/en-US/basic-nodepool/drift)

- Instead follow [6_drift.md](https://github.com/Azure/karpenter-provider-azure/tree/main/docs/workshops/6_drift.md)

### Step: [RightSizing](https://catalog.workshops.aws/karpenter/en-US/basic-nodepool/rightsizing)

- Notes:
    - Things generally align here. However, the logs you see for instance selection will be slightly different, closer to the following:
        ```

        ```

- Adjustments:
    - Only requires [Basic Cheet Sheet](#basic-cheet-sheet)

### Step: [Consolidation](https://catalog.workshops.aws/karpenter/en-US/cost-optimization/consolidation)

- Notes:
    - Spot instances work slightly different within Azure, and Spot-to-Spot Consolidation is not currently supported.

### Step: [Single Node Consolidation](https://catalog.workshops.aws/karpenter/en-US/cost-optimization/consolidation/single-node)

- Adjustments:
    - In initial cleanup, replace the command to cleanup the `ec2nodeclass`, with:
        ```bash
        kubectl delete aksnodeclass default
        ```
    - The same concepts within the workshop generally translate to AKS. However, for the deployment step of the NodePool, use the same deployment command as earlier, found in [9_single_node_consolidation.md](https://github.com/Azure/karpenter-provider-azure/tree/main/docs/workshops/9_single_node_consolidation.md) 

### Step: [Multi Node Consolidation](https://catalog.workshops.aws/karpenter/en-US/cost-optimization/consolidation/multi-node)

- Adjustments:
    - In initial cleanup, replace the command to cleanup the 
    - The same concepts within the workshop generally translate to AKS, with different instances/pricing. However, for the deployment step of the NodePool, use the command found in [10_multi_node_consolidation.md](https://github.com/Azure/karpenter-provider-azure/tree/main/docs/workshops/10_multi_node_consolidation.md)

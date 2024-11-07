
## Deploy NodePool:

Use the following command to deploy a basic `NodePool`, and `AKSNodeClass` to your cluster, with the given config:

- `requirements`: used to restrict the set of node configurations.
    - We've chosen to restrict provisioning to only nodes within the azure sku family [D], that are amd64, linux, and on-demand.
- `disruption`: this can be used to configure how, and when we want disruptions to occur.
    - We've chosen to disrupt under consolidation when nodes are empty or underutilized, but only after `30s`.
- `labels`: these are labels that will be applyed to all of the nodes created by Karpenter in association with the NodePool.
    - We've added the label `aks-workshop: karpenter`, which will be used throughout the rest of the workshop. (For context on the cilium label see the note below.)*
- `limits`: limits can be used to restrict the total resource limits the NodePool is able to provision.
    - We've chosen a `cpu` limit of `10` here. 
- `nodeClassRef`: each NodePool requires a reference to a NodeClass.
    - Here we've referenced the basic `AKSNodeClass` we're creating in the same deployment.
- AKSNodeClass' `spec.imageFamily`: The given imageFamily to use, which AKSNodeClass currently support both `Ubuntu2204`, and `AzureLinux`, with `Ubuntu2204` as the default.
    - We've chosen `Ubuntu2204` here.

*Note: This deployment config is assuming you've setup a cilium cluster, as detailed in `1_install-karpenter.md`. For a cilium cluster, there are specific `labels`, and `startupTaints` requirements configured in the following deployment. They give the karpenter controller the necessicary awareness of cilium to opperate correctly. These are required for any cilium cluster.

```bash
cd ~/environment/karpenter
cat > basic.yaml << EOF
# This example NodePool will provision basic general purpose instances
---
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
    name: default
    annotations:
        kubernetes.io/description: "Basic NodePool for generic workloads"
spec:
    disruption:
        consolidationPolicy: WhenEmptyOrUnderutilized
        consolidateAfter: 1m
    limits:
        cpu: "10"
    template:
        metadata:
            labels:
                # required for Karpenter to predict overhead from cilium DaemonSet
                kubernetes.azure.com/ebpf-dataplane: cilium
                aks-workshop: karpenter
        spec:
            expireAfter: Never
            startupTaints:
                # https://karpenter.sh/docs/concepts/nodepools/#cilium-startup-taint
                - key: node.cilium.io/agent-not-ready
                  effect: NoExecute
                  value: "true"
            requirements:
                - key: karpenter.azure.com/sku-family
                  operator: In
                  values: [D]
                - key: kubernetes.io/arch
                  operator: In
                  values: ["amd64"]
                - key: kubernetes.io/os
                  operator: In
                  values: ["linux"]
                - key: karpenter.sh/capacity-type
                  operator: In
                  values: ["on-demand"]
            nodeClassRef:
                group: karpenter.azure.com
                kind: AKSNodeClass
                name: default
---
apiVersion: karpenter.azure.com/v1alpha2
kind: AKSNodeClass
metadata:
    name: default
    annotations:
        kubernetes.io/description: "Basic AKSNodeClass for running Ubuntu2204 nodes"
spec:
    imageFamily: Ubuntu2204
EOF

kubectl apply -f basic.yaml
```
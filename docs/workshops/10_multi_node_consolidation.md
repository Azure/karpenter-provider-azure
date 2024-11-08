
## Deploy NodePool:

Use the following command to deploy a `NodePool`, and `AKSNodeClass` for Multi Node Consolidation, where we've enabled consolidation for when nodes are empty or underutilized, immediately after `0s`.

```bash
cd ~/environment/karpenter
cat > multinode.yaml << EOF
# This example NodePool will provision general purpose instances
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
        consolidateAfter: 0s
    limits:
        cpu: "100"
    template:
        metadata:
            labels:
                # required for Karpenter to predict overhead from cilium DaemonSet
                kubernetes.azure.com/ebpf-dataplane: cilium
                eks-immersion-team: my-team
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

kubectl apply -f multinode.yaml
```
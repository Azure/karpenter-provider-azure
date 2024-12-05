## Deploy NodePool:

Use the following command to deploy a `NodePool`, and `AKSNodeClass` for `Disruption Controls`, where we've made the nodes `expireAfter` 2 minutes, which will make the NodePool try to remove the nodes after 2 minutes.

> Note: setting `terminationGracePeriod` in addition to `expireAfter` is a good way to help define an absolute maximum lifetime of a node. The node would be deleted at `expireAfter` and finishes draining within the `terminationGracePeriod` thereafter. However, setting `terminationGracePeriod` will ignore `karpenter.sh/do-not-disrupt: "true"`, and take precedence over a pod's own `terminationGracePeriod` or blocking eviction like PDBs, so be careful using it. 

```bash
cd ~/environment/karpenter
cat > eviction.yaml << EOF
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
        consolidationPolicy: WhenEmpty
        consolidateAfter: 30s
    limits:
        cpu: "10"
    template:
        metadata:
            labels:
                # required for Karpenter to predict overhead from cilium DaemonSet
                kubernetes.azure.com/ebpf-dataplane: cilium
                eks-immersion-team: my-team
        spec:
            expireAfter: 2m0s
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

kubectl apply -f eviction.yaml
```

```
nodepool.karpenter.sh/default created
aksnodeclass.karpenter.azure.com/default created
```
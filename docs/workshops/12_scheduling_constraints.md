
## Deploy NodePool:

### 2. Percentage-Base Disruption

Use the following command, instead of the NodePool deployment listed under `2. Percentage-Base Disruption` of `Scheduling Constraints`. This will deploy a `NodePool`, and `AKSNodeClass` where we've set a disruption budget of `40%`.

```bash
cd ~/environment/karpenter
cat > ndb-nodepool.yaml << EOF
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
        consolidateAfter: 30s
        budgets:
        - nodes: "40%"
    limits:
        cpu: "20"
    template:
        metadata:
            labels:
                # required for Karpenter to predict overhead from cilium DaemonSet
                kubernetes.azure.com/ebpf-dataplane: cilium
                eks-immersion-team: my-team
        spec:
            expireAfter: 720h # 30 days
            startupTaints:
                # https://karpenter.sh/docs/concepts/nodepools/#cilium-startup-taint
                - key: node.cilium.io/agent-not-ready
                  effect: NoExecute
                  value: "true"
            requirements:
                - key: karpenter.azure.com/sku-family
                  operator: In
                  values: [D]
                - key: karpenter.azure.com/sku-cpu
                  operator: Lt
                  values: ["3"]
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

kubectl apply -f ndb-nodepool.yaml
```

```
nodepool.karpenter.sh/default created
aksnodeclass.karpenter.azure.com/default created
```

### 3. Multiple Budget Policies

Use the following command, instead of the first NodePool deployment listed under `3. Multiple Budget Policies` of `Scheduling Constraints`. This will update the `NodePool` deployment to add a max disruption budget of `2`, and define a schedule for 3 hours currently set to start at 21:00 UTC (2:00PM PT) of `0` which when active will not allow for any disruption.

> Note: modify the schedule to the current UTC time, to see it take effect while completing this workshop

```bash
cd ~/environment/karpenter
cat > ndb-nodepool.yaml << EOF
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
        consolidateAfter: 30s
        budgets:
        - nodes: "40%"
          reasons:
          - "Empty"
          - "Drifted"
        - nodes: "2"
        - nodes: "0"
          schedule: "0 21 * * *" # modify this line to the current UTC time
          duration: 3h
    limits:
        cpu: "40"
    template:
        metadata:
            labels:
                # required for Karpenter to predict overhead from cilium DaemonSet
                kubernetes.azure.com/ebpf-dataplane: cilium
                eks-immersion-team: my-team
        spec:
            expireAfter: 720h # 30 days
            startupTaints:
                # https://karpenter.sh/docs/concepts/nodepools/#cilium-startup-taint
                - key: node.cilium.io/agent-not-ready
                  effect: NoExecute
                  value: "true"
            requirements:
                - key: karpenter.azure.com/sku-family
                  operator: In
                  values: [D]
                - key: karpenter.azure.com/sku-cpu
                  operator: Lt
                  values: ["3"]
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

kubectl apply -f ndb-nodepool.yaml
```

```
nodepool.karpenter.sh/default configured
aksnodeclass.karpenter.azure.com/default unchanged
```

Use the following command, instead of the second NodePool deployment listed under `3. Multiple Budget Policies` of `Scheduling Constraints`. This will remove the disruption schedule which is not allowing for any disruptions to occur.

```bash
cd ~/environment/karpenter
cat > ndb-nodepool.yaml << EOF
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
        consolidateAfter: 30s
        budgets:
        - nodes: "40%"
          reasons:
          - "Empty"
          - "Drifted"
        - nodes: "2"
    limits:
        cpu: "10"
    template:
        metadata:
            labels:
                # required for Karpenter to predict overhead from cilium DaemonSet
                kubernetes.azure.com/ebpf-dataplane: cilium
                eks-immersion-team: my-team
        spec:
            expireAfter: 720h # 30 days
            startupTaints:
                # https://karpenter.sh/docs/concepts/nodepools/#cilium-startup-taint
                - key: node.cilium.io/agent-not-ready
                  effect: NoExecute
                  value: "true"
            requirements:
                - key: karpenter.azure.com/sku-family
                  operator: In
                  values: [D]
                - key: karpenter.azure.com/sku-cpu
                  operator: Lt
                  values: ["3"]
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

kubectl apply -f ndb-nodepool.yaml
```

```
nodepool.karpenter.sh/default configured
aksnodeclass.karpenter.azure.com/default unchanged
```
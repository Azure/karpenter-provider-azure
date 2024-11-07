Table of contents:
- [Overview](#overview)
- [Test Drift](#test-drift)
  - [Trigger Scaleup](#trigger-scaleup)
  - [Check for Nodes](#check-for-nodes)
  - [Create a new NodeClass](#create-a-new-nodeclass)
  - [Check the Nodes](#check-the-nodes)
  - [Check the Logs](#check-the-logs)
  - [Cleanup](#cleanup)

## Overview

Azure Karpenter creates a hash of the `AKSNodeClass` spec, and stores it under the annotation `karpenter.azure.com/aksnodeclass-hash`. Karpenter will then compare this hash with existing nodes it controls to see if things have drifted from the desired spec.

Note: there are other conditions for Drift to occur, both within the NodePools, and NodeClass. 

## Test Drift

We are going to switch our `imageFamily` in the `AKSNodePool` to `AzureLinux`.

### Trigger Scaleup

First, lets launch a deployment of pods to trigger a scaleup using the following command:
```bash
cd ~/environment/karpenter
cat > drift-deploy.yaml << EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: inflate
  namespace: workshop
spec:
  replicas: 5
  selector:
    matchLabels:
      app: inflate
  template:
    metadata:
      labels:
        app: inflate
    spec:
      terminationGracePeriodSeconds: 0
      containers:
        - image: mcr.microsoft.com/oss/kubernetes/pause:3.6
          name: inflate
          resources:
            requests:
              cpu: "1"
      nodeSelector:
        eks-immersion-team: my-team
EOF

kubectl apply -f drift-deploy.yaml
```

### Check for Nodes

```bash
kubectl get nodes -l eks-immersion-team=my-team
```

### Create a new NodeClass

```bash
cd ~/environment/karpenter
cat > new-nodeclass.yaml << EOF
apiVersion: karpenter.azure.com/v1alpha2
kind: AKSNodeClass
metadata:
    name: newnodeclass
    annotations:
        kubernetes.io/description: "Basic AKSNodeClass for running AzureLinux nodes"
spec:
    imageFamily: AzureLinux
EOF

kubectl apply -f new-nodeclass.yaml
```

### Patch the nodeClassRef

```bash
kubectl patch nodepool default --type='json' -p '[{"op": "replace", "path": "/spec/template/spec/nodeClassref/name", "value":"newnodeclass"}]
```

### Check the Nodes

```bash
kubectl get nodeclaims
```

You should see a new nodeclaim has been created.

After a little while you should see the new node show up, and the old instance be removed.

```bash
kubectl get nodes -l eks-immersion-team=my-team
```

### Check the Logs

Inspecting the logs you can see the specific drift messages

```bash
kubectl logs -n "${KARPENTER_NAMESPACE}" --tail=100 -l app.kubernetes.io/name=karpenter | grep -i drift | jq
```

### Cleanup

Delete the drift deployment:

```bash
kubectl delete -f drift-deploy.yaml
```

Switch the NodePool `nodeClassRef` back to the default AKSNodeClass

```bash
kubectl patch nodepool default --type='json' -p '[{"op": "replace", "path": "/spec/template/spec/nodeClassref/name", "value":"default"}]
```

Delete the new AKSNodeClass

```bash
kubectl apply -f new-nodeclass.yaml
```

Remove the extra files created for this test

```bash
cd ~/environment/karpenter
rm drift-deploy.yaml
rm new-nodeclass.yaml
```

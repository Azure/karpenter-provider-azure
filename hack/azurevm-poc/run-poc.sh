#!/bin/bash
# AzureVM Provision Mode PoC — End-to-End Demo
# This script creates a k3s cluster on Azure, deploys karpenter in azurevm mode,
# and triggers VM provisioning via AzureNodeClass + NodePool + pending pod.
#
# Prerequisites:
#   - Azure CLI authenticated (az login)
#   - Go 1.25+ installed
#   - kubectl installed
#   - SSH key pair at ~/.ssh/id_rsa (or adjust --generate-ssh-keys)
#
# Usage: ./hack/azurevm-poc/run-poc.sh [RESOURCE_GROUP] [LOCATION]
set -euo pipefail

RG="${1:-karpenter-azurevm-poc}"
LOCATION="${2:-westus2}"
VNET="karpenter-vnet"
SUBNET="karpenter-subnet"
VM_NAME="k3s-cp"
SUB_ID=$(az account show --query id -o tsv)
SUBNET_ID="/subscriptions/$SUB_ID/resourceGroups/$RG/providers/Microsoft.Network/virtualNetworks/$VNET/subnets/$SUBNET"

echo "=== AzureVM PoC — $RG in $LOCATION ==="
echo "Subscription: $SUB_ID"

# 1. Create infrastructure
echo "[1/8] Creating resource group and VNet..."
az group create --name "$RG" --location "$LOCATION" -o none
az network vnet create --resource-group "$RG" --name "$VNET" \
  --address-prefix 10.0.0.0/16 --subnet-name "$SUBNET" --subnet-prefix 10.0.0.0/24 -o none

echo "[2/8] Creating k3s VM..."
VM_IP=$(az vm create --resource-group "$RG" --name "$VM_NAME" --image Ubuntu2404 \
  --size Standard_D4s_v3 --vnet-name "$VNET" --subnet "$SUBNET" \
  --admin-username azureuser --generate-ssh-keys --public-ip-sku Standard \
  --query publicIpAddress -o tsv)
echo "VM IP: $VM_IP"

# Grant VM identity permissions
VM_PRINCIPAL=$(az vm show --resource-group "$RG" --name "$VM_NAME" --query identity.principalId -o tsv)
az role assignment create --assignee "$VM_PRINCIPAL" --role Contributor \
  --scope "/subscriptions/$SUB_ID/resourceGroups/$RG" -o none
az role assignment create --assignee "$VM_PRINCIPAL" --role Reader \
  --scope "/subscriptions/$SUB_ID" -o none
echo "RBAC assigned"

# Open k8s API port
az network nsg rule create --resource-group "$RG" --nsg-name "${VM_NAME}NSG" \
  --name allow-k8s-api --priority 100 --destination-port-ranges 6443 \
  --protocol Tcp --access Allow -o none

# 2. Install k3s
echo "[3/8] Installing k3s..."
ssh -o StrictHostKeyChecking=no azureuser@"$VM_IP" \
  "sudo mkdir -p /etc/rancher/k3s && echo 'tls-san: $VM_IP' | sudo tee /etc/rancher/k3s/config.yaml && \
   curl -sfL https://get.k3s.io | sudo sh - && sleep 5 && sudo kubectl get nodes"

# Get kubeconfig
ssh azureuser@"$VM_IP" "sudo cat /etc/rancher/k3s/k3s.yaml" | \
  sed "s/127.0.0.1/$VM_IP/" > /tmp/k3s-kubeconfig.yaml
export KUBECONFIG=/tmp/k3s-kubeconfig.yaml
kubectl get nodes

# 3. Build karpenter
echo "[4/8] Building karpenter controller..."
SCRIPT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$SCRIPT_DIR"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/karpenter-controller ./cmd/controller/
echo "Binary: $(ls -lh /tmp/karpenter-controller | awk '{print $5}')"

# 4. Deploy binary
echo "[5/8] Uploading binary to VM..."
scp /tmp/karpenter-controller azureuser@"$VM_IP":/tmp/karpenter-controller
ssh azureuser@"$VM_IP" "sudo cp /tmp/karpenter-controller /usr/local/bin/ && sudo chmod +x /usr/local/bin/karpenter-controller"

# 5. Apply CRDs
echo "[6/8] Applying CRDs..."
kubectl apply -f "$SCRIPT_DIR/pkg/apis/crds/"

# 6. Deploy controller
echo "[7/8] Deploying karpenter controller..."
kubectl create namespace karpenter 2>/dev/null || true
kubectl apply -f - <<EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: karpenter
  namespace: karpenter
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: karpenter
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cluster-admin
subjects:
- kind: ServiceAccount
  name: karpenter
  namespace: karpenter
---
apiVersion: v1
kind: Pod
metadata:
  name: karpenter-controller
  namespace: karpenter
spec:
  serviceAccountName: karpenter
  hostNetwork: true
  containers:
  - name: controller
    image: ubuntu:24.04
    command: ["/host-bin/karpenter-controller"]
    args:
    - "--provision-mode=azurevm"
    - "--vnet-subnet-id=$SUBNET_ID"
    - "--node-resource-group=$RG"
    env:
    - name: ARM_SUBSCRIPTION_ID
      value: "$SUB_ID"
    - name: AZURE_SUBSCRIPTION_ID
      value: "$SUB_ID"
    - name: AZURE_NODE_RESOURCE_GROUP
      value: "$RG"
    - name: LOCATION
      value: "$LOCATION"
    - name: ARM_USE_MANAGED_IDENTITY_EXTENSION
      value: "true"
    - name: SSL_CERT_DIR
      value: "/host-certs"
    volumeMounts:
    - name: host-bin
      mountPath: /host-bin
      readOnly: true
    - name: host-certs
      mountPath: /host-certs
      readOnly: true
    - name: host-ca-bundle
      mountPath: /etc/ssl/certs
      readOnly: true
  volumes:
  - name: host-bin
    hostPath: {path: /usr/local/bin, type: Directory}
  - name: host-certs
    hostPath: {path: /etc/ssl/certs, type: Directory}
  - name: host-ca-bundle
    hostPath: {path: /usr/share/ca-certificates, type: Directory}
  tolerations:
  - operator: Exists
EOF

echo "Waiting for controller to start..."
sleep 20
kubectl logs -n karpenter karpenter-controller --tail=5

# 7. Create AzureNodeClass + NodePool + pending pod
echo "[8/8] Creating AzureNodeClass, NodePool, and test pod..."
kubectl apply -f - <<EOF
apiVersion: karpenter.azure.com/v1alpha1
kind: AzureNodeClass
metadata:
  name: poc-azurevm
spec:
  imageID: "/subscriptions/$SUB_ID/resourceGroups/$RG/providers/Microsoft.Compute/images/poc-image"
  userData: "IyEvYmluL2Jhc2gKZWNobyBoZWxsbw=="
  vnetSubnetID: "$SUBNET_ID"
  osDiskSizeGB: 128
  instanceTypes:
    - Standard_D2s_v3
  tags:
    poc: azurevm
---
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: poc-pool
spec:
  template:
    spec:
      nodeClassRef:
        group: karpenter.azure.com
        kind: AzureNodeClass
        name: poc-azurevm
      requirements:
        - key: kubernetes.io/arch
          operator: In
          values: ["amd64"]
  disruption:
    consolidationPolicy: WhenEmpty
    consolidateAfter: 30s
---
apiVersion: v1
kind: Pod
metadata:
  name: poc-pending
spec:
  containers:
  - name: pause
    image: registry.k8s.io/pause:3.9
    resources:
      requests:
        cpu: "1"
        memory: "1Gi"
  nodeSelector:
    karpenter.sh/nodepool: poc-pool
  tolerations:
  - operator: Exists
EOF

echo ""
echo "=== PoC deployed! ==="
echo "Watch provisioning: kubectl logs -n karpenter karpenter-controller -f"
echo "Check NodeClaims:   kubectl get nodeclaims"
echo "Check VMs:          az vm list -g $RG -o table"
echo ""
echo "Cleanup: az group delete --name $RG --yes --no-wait"

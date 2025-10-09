AZURE_LOCATION ?= westus2
AZURE_VM_SIZE ?= ""
COMMON_NAME ?= karpenter
ifeq ($(CODESPACES),true)
  AZURE_RESOURCE_GROUP ?= $(CODESPACE_NAME)
  AZURE_ACR_NAME ?= $(subst -,,$(CODESPACE_NAME))
else
  NAME_SUFFIX ?= $(shell git config user.email | cut -d'@' -f1 | tr -d '+')
  AZURE_RESOURCE_GROUP ?= $(COMMON_NAME)$(NAME_SUFFIX)
  AZURE_ACR_NAME ?= $(COMMON_NAME)$(NAME_SUFFIX)
endif

AZURE_ACR_SUFFIX ?= azurecr.io
AZURE_SIG_SUBSCRIPTION_ID ?= 10945678-1234-1234-1234-123456789012
AZURE_CLUSTER_NAME ?= $(COMMON_NAME)
AZURE_RESOURCE_GROUP_MC = MC_$(AZURE_RESOURCE_GROUP)_$(AZURE_CLUSTER_NAME)_$(AZURE_LOCATION)

KARPENTER_SERVICE_ACCOUNT_NAME ?= karpenter-sa
AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME ?= karpentermsi
KARPENTER_FEDERATED_IDENTITY_CREDENTIAL_NAME ?= KARPENTER_FID

CUSTOM_VNET_NAME ?= $(AZURE_CLUSTER_NAME)-vnet
CUSTOM_SUBNET_NAME ?= nodesubnet

AKS_MACHINES_POOL_NAME ?= testmpool

.DEFAULT_GOAL := help	# make without arguments will show help

az-all:              az-login az-create-workload-msi az-mkaks-cilium      az-create-federated-cred az-perm               az-perm-acr az-configure-values             az-build az-run          az-run-sample ## Provision the infra (ACR,AKS); build and deploy Karpenter; deploy sample Provisioner and workload

az-all-cniv1:        az-login az-create-workload-msi az-mkaks-cniv1       az-create-federated-cred az-perm               az-perm-acr az-configure-values             az-build az-run          az-run-sample ## Provision the infra (ACR,AKS); build and deploy Karpenter; deploy sample Provisioner and workload

az-all-cni-overlay:  az-login az-create-workload-msi az-mkaks-overlay     az-create-federated-cred az-perm               az-perm-acr az-configure-values             az-build az-run          az-run-sample ## Provision the infra (ACR,AKS); build and deploy Karpenter; deploy sample Provisioner and workload

az-all-aksmachine:   az-login az-create-workload-msi az-mkaks-cilium      az-create-federated-cred az-perm               az-perm-acr az-perm-aksmachine             az-add-aksmachinespool az-configure-values-aksmachine             az-build az-run          az-run-sample

## Saved: az-create-workload-msi az-mkaks-cilium-userassigned      az-create-federated-cred az-perm               az-perm-acr az-perm-aksmachine             az-configure-values-aksmachine             az-build az-run

az-all-perftest:     az-login az-create-workload-msi az-mkaks-perftest    az-create-federated-cred az-perm               az-perm-acr az-configure-values
	$(MAKE) az-mon-deploy
	$(MAKE) az-pprof-enable
	yq -i '.manifests.helm.releases[0].overrides.controller.resources.requests = {"cpu":4,"memory":"3Gi"}' skaffold.yaml
	yq -i '.manifests.helm.releases[0].overrides.controller.resources.limits   = {"cpu":4,"memory":"3Gi"}' skaffold.yaml
	$(MAKE) az-run
	$(MAKE) az-taintsystemnodes
	kubectl apply -f examples/v1/perftest.yaml
	kubectl apply -f examples/workloads/inflate.yaml
	# make az-mon-access

az-all-custom-vnet:  az-login az-create-workload-msi az-mkaks-custom-vnet az-create-federated-cred az-perm-subnet-custom az-perm-acr az-configure-values az-build az-run          az-run-sample ## Provision the infra (ACR,AKS); build and deploy Karpenter; deploy sample Provisioner and workload
az-all-user:	     az-login                        az-mkaks-user                                                                   az-configure-values             az-helm-install-snapshot az-run-sample ## Provision the cluster and deploy Karpenter snapshot release
# TODO: az-all-savm case is not currently built to support workload identity, need to re-evaluate
az-all-savm:         az-login                        az-mkaks-savm                                 az-perm-savm                      az-configure-values             az-build az-run          az-run-sample ## Provision the infra (ACR,AKS); build and deploy Karpenter; deploy sample Provisioner and workload - StandaloneVirtualMachines

az-login: ## Login into Azure
	az account show -o none || az login
	az account set --subscription $(AZURE_SUBSCRIPTION_ID)

az-mkrg: ## Create resource group
	if az group exists --name $(AZURE_RESOURCE_GROUP) | grep -qi "false"; then \
		az group create --name $(AZURE_RESOURCE_GROUP) --location $(AZURE_LOCATION) -o none; \
	fi

az-mkacr: az-mkrg ## Create test ACR
	az acr create --name $(AZURE_ACR_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --location $(AZURE_LOCATION) \
		--sku Basic --admin-enabled -o none
	az acr login  --name $(AZURE_ACR_NAME)

az-acrimport: ## Imports an image to an acr registry
	az acr import --name $(AZURE_ACR_NAME) --source "mcr.microsoft.com/oss/kubernetes/pause:3.6" --image "pause:3.6"

az-cleanenv: az-rmnodeclaims-fin az-rmnodeclasses-fin ## Deletes a few common karpenter testing resources(pods, nodepools, nodeclaims, aksnodeclasses)
	kubectl delete deployments -n default --all
	kubectl delete pods -n default --all
	kubectl delete nodeclaims --all
	kubectl delete nodepools --all
	kubectl delete aksnodeclasses --all

az-mkaks: az-mkacr ## Create test AKS cluster (with --vm-set-type AvailabilitySet for compatibility with standalone VMs)
	az aks create          --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --attach-acr $(AZURE_ACR_NAME) --location $(AZURE_LOCATION) \
		--enable-managed-identity --node-count 3 --generate-ssh-keys --vm-set-type AvailabilitySet -o none $(if $(AZURE_VM_SIZE),--node-vm-size $(AZURE_VM_SIZE),)
	az aks get-credentials --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --overwrite-existing
	skaffold config set default-repo $(AZURE_ACR_NAME).$(AZURE_ACR_SUFFIX)/karpenter

az-mkaks-cniv1: az-mkacr ## Create test AKS cluster (with --network-plugin azure)
	az aks create          --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --attach-acr $(AZURE_ACR_NAME) \
		--enable-managed-identity --node-count 3 --generate-ssh-keys -o none --network-plugin azure \
		--enable-oidc-issuer --enable-workload-identity $(if $(AZURE_VM_SIZE),--node-vm-size $(AZURE_VM_SIZE),)
	az aks get-credentials --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --overwrite-existing
	skaffold config set default-repo $(AZURE_ACR_NAME).$(AZURE_ACR_SUFFIX)/karpenter


az-mkaks-cilium: az-mkacr ## Create test AKS cluster (with --network-dataplane cilium, --network-plugin azure, and --network-plugin-mode overlay)
	az aks create          --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --attach-acr $(AZURE_ACR_NAME) \
		--enable-managed-identity --node-count 3 --generate-ssh-keys -o none --network-dataplane cilium --network-plugin azure --network-plugin-mode overlay \
		--enable-oidc-issuer --enable-workload-identity $(if $(AZURE_VM_SIZE),--node-vm-size $(AZURE_VM_SIZE),)
	az aks get-credentials --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --overwrite-existing
	skaffold config set default-repo $(AZURE_ACR_NAME).$(AZURE_ACR_SUFFIX)/karpenter

az-mkaks-cilium-userassigned: az-mkacr az-create-workload-msi ## Create test AKS cluster with user-assigned identity (supports custom kubelet identity)
	$(eval KARPENTER_USER_ASSIGNED_IDENTITY_ID=$(shell az identity show --resource-group "${AZURE_RESOURCE_GROUP}" --name "${AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME}" --query 'id' -otsv))
	az aks create          --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --attach-acr $(AZURE_ACR_NAME) \
		--assign-identity $(KARPENTER_USER_ASSIGNED_IDENTITY_ID) --node-count 3 --generate-ssh-keys -o none --network-dataplane cilium --network-plugin azure --network-plugin-mode overlay \
		--enable-oidc-issuer --enable-workload-identity $(if $(AZURE_VM_SIZE),--node-vm-size $(AZURE_VM_SIZE),)
	az aks get-credentials --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --overwrite-existing
	skaffold config set default-repo $(AZURE_ACR_NAME).$(AZURE_ACR_SUFFIX)/karpenter

az-mkaks-overlay: az-mkacr ## Create test AKS cluster (with --network-plugin-mode overlay)
	az aks create          --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --attach-acr $(AZURE_ACR_NAME) \
		--enable-managed-identity --node-count 3 --generate-ssh-keys -o none --network-plugin azure --network-plugin-mode overlay \
		--enable-oidc-issuer --enable-workload-identity $(if $(AZURE_VM_SIZE),--node-vm-size $(AZURE_VM_SIZE),)
	az aks get-credentials --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --overwrite-existing
	skaffold config set default-repo $(AZURE_ACR_NAME).$(AZURE_ACR_SUFFIX)/karpenter

az-mkaks-perftest: az-mkacr ## Create test AKS cluster (with Azure Overlay, larger system pool VMs and larger pod-cidr)
	az aks create          --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --attach-acr $(AZURE_ACR_NAME) \
		--enable-managed-identity --node-count 2 --generate-ssh-keys -o none --network-plugin azure --network-plugin-mode overlay \
		--enable-oidc-issuer --enable-workload-identity \
		--node-vm-size $(if $(AZURE_VM_SIZE),$(AZURE_VM_SIZE),Standard_D16s_v6) --pod-cidr "10.128.0.0/11"
	az aks get-credentials --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --overwrite-existing
	skaffold config set default-repo $(AZURE_ACR_NAME).$(AZURE_ACR_SUFFIX)/karpenter

az-mkvnet: ## Create a VNet with address range of 10.1.0.0/16
	az network vnet create --name $(CUSTOM_VNET_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --location $(AZURE_LOCATION) --address-prefixes "10.1.0.0/16"

az-mksubnet:  ## Create a subnet with address range of 10.1.0.0/24
	az network vnet subnet create --name $(CUSTOM_SUBNET_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --vnet-name $(CUSTOM_VNET_NAME) --address-prefixes "10.1.0.0/24"

az-mkaks-custom-vnet: az-mkacr az-mkvnet az-mksubnet ## Create test AKS cluster with custom VNet
	az aks create --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --attach-acr $(AZURE_ACR_NAME) \
		--enable-managed-identity --node-count 3 --generate-ssh-keys -o none --network-dataplane cilium --network-plugin azure --network-plugin-mode overlay \
		--enable-oidc-issuer --enable-workload-identity $(if $(AZURE_VM_SIZE),--node-vm-size $(AZURE_VM_SIZE),) \
		--vnet-subnet-id "/subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP)/providers/Microsoft.Network/virtualNetworks/$(CUSTOM_VNET_NAME)/subnets/$(CUSTOM_SUBNET_NAME)"
	az aks get-credentials --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --overwrite-existing
	skaffold config set default-repo $(AZURE_ACR_NAME).$(AZURE_ACR_SUFFIX)/karpenter

az-create-workload-msi: az-mkrg
	# create the workload MSI that is the backing for the karpenter pod auth
	az identity create --name "${AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME}" --resource-group "${AZURE_RESOURCE_GROUP}" --location "${AZURE_LOCATION}"

az-create-federated-cred:
	$(eval AKS_OIDC_ISSUER=$(shell az aks show -n "${AZURE_CLUSTER_NAME}" -g "${AZURE_RESOURCE_GROUP}" --query "oidcIssuerProfile.issuerUrl" -otsv))

	# create federated credential linked to the karpenter service account for auth usage
	az identity federated-credential create --name ${KARPENTER_FEDERATED_IDENTITY_CREDENTIAL_NAME} --identity-name "${AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME}" --resource-group "${AZURE_RESOURCE_GROUP}" --issuer "${AKS_OIDC_ISSUER}" --subject system:serviceaccount:"${KARPENTER_NAMESPACE}":"${KARPENTER_SERVICE_ACCOUNT_NAME}" --audience api://AzureADTokenExchange

az-mkaks-savm: az-mkrg ## Create experimental cluster with standalone VMs (+ ACR)
	az deployment group create --resource-group $(AZURE_RESOURCE_GROUP) --template-file hack/azure/aks-savm.bicep --parameters aksname=$(AZURE_CLUSTER_NAME) acrname=$(AZURE_ACR_NAME)
	az aks get-credentials --resource-group $(AZURE_RESOURCE_GROUP) --name $(AZURE_CLUSTER_NAME) --overwrite-existing
	skaffold config set default-repo $(AZURE_ACR_NAME).$(AZURE_ACR_SUFFIX)/karpenter

az-add-aksmachinespool:
	hack/deploy/add-aks-machines-pool.sh $(AZURE_SUBSCRIPTION_ID) $(AZURE_RESOURCE_GROUP) $(AZURE_CLUSTER_NAME) $(AKS_MACHINES_POOL_NAME)

az-rmrg: ## Destroy test ACR and AKS cluster by deleting the resource group (use with care!)
	az group delete --name $(AZURE_RESOURCE_GROUP)

az-configure-values:  ## Generate cluster-related values for Karpenter Helm chart
	hack/deploy/configure-values.sh $(AZURE_CLUSTER_NAME) $(AZURE_RESOURCE_GROUP) $(KARPENTER_SERVICE_ACCOUNT_NAME) $(AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME) $(PROVISION_MODE)

az-configure-values-aksmachine:  ## Generate cluster-related values for Karpenter Helm chart
	hack/deploy/configure-values.sh $(AZURE_CLUSTER_NAME) $(AZURE_RESOURCE_GROUP) $(KARPENTER_SERVICE_ACCOUNT_NAME) $(AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME) aksmachineapi $(AKS_MACHINES_POOL_NAME)

az-mkvmssflex: ## Create VMSS Flex (optional, only if creating VMs referencing this VMSS)
	az vmss create --name $(AZURE_CLUSTER_NAME)-vmss --resource-group $(AZURE_RESOURCE_GROUP_MC) --location $(AZURE_LOCATION) \
		--instance-count 0 --orchestration-mode Flexible --platform-fault-domain-count 1 --zones 1 2 3

az-rmvmss-vms: ## Delete all VMs in VMSS Flex (use with care!)
	az vmss delete-instances --name $(AZURE_CLUSTER_NAME)-vmss --resource-group $(AZURE_RESOURCE_GROUP_MC) --instance-ids '*'

az-perm: ## Create role assignments to let Karpenter manage VMs and Network
	# Note: need to be principalId for E2E workflow as the pipeline identity doesn't have permissions to "query Graph API"
	$(eval KARPENTER_USER_ASSIGNED_CLIENT_ID=$(shell az identity show --resource-group "${AZURE_RESOURCE_GROUP}" --name "${AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME}" --query 'principalId' -otsv))
	az role assignment create --assignee-object-id $(KARPENTER_USER_ASSIGNED_CLIENT_ID) --assignee-principal-type "ServicePrincipal" --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP_MC) --role "Virtual Machine Contributor"
	az role assignment create --assignee-object-id $(KARPENTER_USER_ASSIGNED_CLIENT_ID) --assignee-principal-type "ServicePrincipal" --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP_MC) --role "Network Contributor"
	az role assignment create --assignee-object-id $(KARPENTER_USER_ASSIGNED_CLIENT_ID) --assignee-principal-type "ServicePrincipal" --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP_MC) --role "Managed Identity Operator"
	@echo Consider "make az-configure-values"!

az-perm-aksmachine: ## Create role assignments for AKS machine API operations
	$(eval KARPENTER_USER_ASSIGNED_CLIENT_ID=$(shell az identity show --resource-group "${AZURE_RESOURCE_GROUP}" --name "${AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME}" --query 'principalId' -otsv))
	az role assignment create --assignee-object-id $(KARPENTER_USER_ASSIGNED_CLIENT_ID) --assignee-principal-type "ServicePrincipal" --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP) --role "Azure Kubernetes Service Contributor Role"
	az role assignment create --assignee-object-id $(KARPENTER_USER_ASSIGNED_CLIENT_ID) --assignee-principal-type "ServicePrincipal" --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP_MC) --role "Network Contributor"
	$(eval CLUSTER_IDENTITY_TYPE=$(shell az aks show --resource-group "${AZURE_RESOURCE_GROUP}" --name "${AZURE_CLUSTER_NAME}" --query 'identity.type' -otsv))
	$(eval CLUSTER_IDENTITY=$(shell if [ "$(CLUSTER_IDENTITY_TYPE)" = "UserAssigned" ]; then echo "$(KARPENTER_USER_ASSIGNED_CLIENT_ID)"; else az aks show --resource-group "${AZURE_RESOURCE_GROUP}" --name "${AZURE_CLUSTER_NAME}" --query 'identity.principalId' -otsv; fi))
	az role assignment create --assignee-object-id $(CLUSTER_IDENTITY) --assignee-principal-type "ServicePrincipal" --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP_MC) --role "Virtual Machine Contributor"
	az role assignment create --assignee-object-id $(CLUSTER_IDENTITY) --assignee-principal-type "ServicePrincipal" --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP_MC) --role "Network Contributor"
	az role assignment create --assignee-object-id $(CLUSTER_IDENTITY) --assignee-principal-type "ServicePrincipal" --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP_MC) --role "Managed Identity Operator"

az-perm-sig: ## Create role assignments when testing with SIG images
	$(eval KARPENTER_USER_ASSIGNED_CLIENT_ID=$(shell az identity show --resource-group "${AZURE_RESOURCE_GROUP}" --name "${AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME}" --query 'principalId' -otsv))
	az role assignment create --assignee-object-id $(KARPENTER_USER_ASSIGNED_CLIENT_ID) --assignee-principal-type "ServicePrincipal" --role "Reader" --scope /subscriptions/$(AZURE_SIG_SUBSCRIPTION_ID)/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu
	az role assignment create --assignee-object-id $(KARPENTER_USER_ASSIGNED_CLIENT_ID) --assignee-principal-type "ServicePrincipal" --role "Reader" --scope /subscriptions/$(AZURE_SIG_SUBSCRIPTION_ID)/resourceGroups/AKS-AzureLinux/providers/Microsoft.Compute/galleries/AKSAzureLinux

az-perm-subnet-custom: az-perm ## Create role assignments to let Karpenter manage VMs and Network (custom VNet)
	$(eval VNET_SUBNET_ID=$(shell az aks show --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) | jq -r ".agentPoolProfiles[0].vnetSubnetId"))
	$(eval KARPENTER_USER_ASSIGNED_CLIENT_ID=$(shell az identity show --resource-group "${AZURE_RESOURCE_GROUP}" --name "${AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME}" --query 'principalId' -otsv))
	$(eval SUBNET_RESOURCE_GROUP=$(shell az network vnet subnet show --id $(VNET_SUBNET_ID) | jq -r ".resourceGroup"))
	az role assignment create --assignee-object-id $(KARPENTER_USER_ASSIGNED_CLIENT_ID) --assignee-principal-type "ServicePrincipal" --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(SUBNET_RESOURCE_GROUP) --role "Network Contributor"

az-perm-savm: ## Create role assignments to let Karpenter manage VMs and Network
	# Note: savm has not been converted over to use a workload identity
	$(eval AZURE_OBJECT_ID=$(shell az aks show --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) | jq  -r ".identityProfile.kubeletidentity.objectId"))
	az role assignment create --assignee-object-id $(AZURE_OBJECT_ID) --assignee-principal-type "ServicePrincipal" --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP_MC) --role "Virtual Machine Contributor"
	az role assignment create --assignee-object-id $(AZURE_OBJECT_ID) --assignee-principal-type "ServicePrincipal" --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP_MC) --role "Network Contributor"
	az role assignment create --assignee-object-id $(AZURE_OBJECT_ID) --assignee-principal-type "ServicePrincipal" --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP_MC) --role "Managed Identity Operator"
	az role assignment create --assignee-object-id $(AZURE_OBJECT_ID) --assignee-principal-type "ServicePrincipal" --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP)    --role "Network Contributor" # in some case we create vnet here
	@echo Consider "make az-configure-values"!

az-perm-acr:
	$(eval KARPENTER_USER_ASSIGNED_CLIENT_ID=$(shell az identity show --resource-group "${AZURE_RESOURCE_GROUP}" --name "${AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME}" --query 'principalId' -otsv))
	$(eval AZURE_ACR_ID=$(shell    az acr show --name $(AZURE_ACR_NAME)     --resource-group $(AZURE_RESOURCE_GROUP) | jq  -r ".id"))
	az role assignment create --assignee-object-id $(KARPENTER_USER_ASSIGNED_CLIENT_ID) --assignee-principal-type "ServicePrincipal" --scope $(AZURE_ACR_ID) --role "AcrPull"

az-aks-check-acr:
	az aks check-acr --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --acr $(AZURE_ACR_NAME)

az-build: ## Build the Karpenter controller and webhook images using skaffold build (which uses ko build)
	az acr login -n $(AZURE_ACR_NAME)
	skaffold build

az-creds: ## Get cluster credentials
	az aks get-credentials --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP)

az-run: ## Deploy the controller from the current state of your git repository into your ~/.kube/config cluster using skaffold run
	az acr login -n $(AZURE_ACR_NAME)
	skaffold run

az-run-sample: ## Deploy sample Provisioner and workload (with 0 replicas, to be scaled manually)
	kubectl apply -f examples/v1/general-purpose.yaml
	kubectl apply -f examples/workloads/inflate.yaml

az-mc-show: ## show managed cluster
	az aks show --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP)

az-mc-upgrade: ## upgrade managed cluster
	$(eval UPGRADE_K8S_VERSION=$(shell az aks get-upgrades --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) | jq -r ".controlPlaneProfile.upgrades[0].kubernetesVersion"))
	az aks upgrade --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --kubernetes-version $(UPGRADE_K8S_VERSION)

az-dev: ## Deploy and develop using skaffold dev
	skaffold dev

az-debug: ## Rebuild, deploy and debug using skaffold debug
	az acr login -n $(AZURE_ACR_NAME)
	skaffold delete || true
	skaffold debug # --platform=linux/arm64

az-debug-bootstrap: ## Debug bootstrap (target first privateIP of the first NIC with Karpenter tag)
	$(eval JUMP_NODE=$(shell kubectl get nodes -o name | head -n 1))
	$(eval JUMP_POD=$(shell kubectl debug $(JUMP_NODE) --image kroniak/ssh-client -- sh -c "mkdir /root/.ssh; sleep 1h" | cut -d' ' -f4))
	kubectl wait --for=condition=Ready pod/$(JUMP_POD)
	kubectl cp ~/.ssh/id_rsa $(JUMP_POD):/root/.ssh/id_rsa
	$(eval NODE_IP=$(shell az network nic list -g $(AZURE_RESOURCE_GROUP_MC) \
		--query '[?tags."karpenter.azure.com_cluster"]|[0].ipConfigurations[0].privateIPAddress'))
	kubectl exec $(JUMP_POD) -it -- ssh -o StrictHostKeyChecking=accept-new azureuser@$(NODE_IP)

az-cleanup: ## Delete the deployment
	skaffold delete || true

az-deploy-goldpinger: ## Deploy goldpinger for testing networking
	kubectl apply -f https://gist.githubusercontent.com/paulgmiller/084bd4605f1661a329e5ab891a826ae0/raw/94a32d259e137bb300ac8af3ef71caa471463f23/goldpinger-daemon.yaml
	kubectl apply -f https://gist.githubusercontent.com/paulgmiller/7bca68cd08cccb4e9bc72b0a08485edf/raw/d6a103fb79a65083f6555e4d822554ed64f510f8/goldpinger-deploy.yaml

az-mon-deploy: ## Deploy monitoring stack (w/o node-exporter)
	helm repo add grafana-charts https://grafana.github.io/helm-charts
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
	helm repo update
	kubectl create namespace monitoring || true
	helm install --namespace monitoring prometheus prometheus-community/prometheus \
		--values hack/monitoring/prometheus-values.yaml
	helm install --namespace monitoring pyroscope grafana-charts/pyroscope \
		--set pyroscope.extraArgs.'usage-stats\.enabled'=false
	helm install --namespace monitoring grafana grafana-charts/grafana \
		--values hack/monitoring/grafana-values.yaml \
		--set env.GF_AUTH_ANONYMOUS_ENABLED=true \
		--set env.GF_AUTH_ANONYMOUS_ORG_ROLE=Admin

az-mon-access: ## Get Grafana admin password and forward port
	@echo Consider running port forward outside of codespace ...
	$(eval POD_NAME=$(shell kubectl get pods --namespace monitoring -l "app.kubernetes.io/name=grafana,app.kubernetes.io/instance=grafana" -o jsonpath="{.items[0].metadata.name}"))
	kubectl port-forward --namespace monitoring $(POD_NAME) 3000

az-mon-cleanup: ## Delete monitoring stack
	helm delete --namespace monitoring grafana
	helm delete --namespace monitoring pyroscope
	helm delete --namespace monitoring prometheus

az-mkgohelper: ## Build and configure custom go-helper-image for skaffold
	cd hack/go-helper-image; docker build . --tag $(AZURE_ACR_NAME).$(AZURE_ACR_SUFFIX)/skaffold-debug-support/go # --platform=linux/arm64
	az acr login -n $(AZURE_ACR_NAME)
	docker push $(AZURE_ACR_NAME).$(AZURE_ACR_SUFFIX)/skaffold-debug-support/go
	skaffold config set --global debug-helpers-registry $(AZURE_ACR_NAME).$(AZURE_ACR_SUFFIX)/skaffold-debug-support

az-rmnodes-fin: ## Remove Karpenter finalizer from all nodes (use with care!)
	for node in $$(kubectl get nodes -l karpenter.sh/nodepool --output=jsonpath={.items..metadata.name}); do \
		kubectl patch node $$node -p '{"metadata":{"finalizers":null}}'; \
	done

az-rmnodes: ## kubectl delete all Karpenter-provisioned nodes; don't wait for finalizers (use with care!)
	kubectl delete --wait=false nodes -l karpenter.sh/nodepool
    # kubectl wait --for=delete nodes -l karpenter.sh/nodepool --timeout=10m

az-rmnodeclaims-fin: ## Remove Karpenter finalizer from all nodeclaims (use with care!)
	for nodeclaim in $$(kubectl get nodeclaims --output=jsonpath={.items..metadata.name}); do \
		kubectl patch nodeclaim $$nodeclaim --type=json -p '[{"op": "remove", "path": "/metadata/finalizers"}]'; \
	done

az-rmnodeclasses-fin: ## Remove Karpenter finalizer from all nodeclasses (use with care!)
	for nodeclass in $$(kubectl get aksnodeclasses --output=jsonpath={.items..metadata.name}); do \
		kubectl patch aksnodeclass $$nodeclass --type=json -p '[{"op": "remove", "path": "/metadata/finalizers"}]'; \
	done

az-rmnodeclaims: ## kubectl delete all nodeclaims; don't wait for finalizers (use with care!)
	kubectl delete --wait=false nodeclaims --all

az-taintsystemnodes: ## Taint all system nodepool nodes
	kubectl taint nodes CriticalAddonsOnly=true:NoSchedule --selector='kubernetes.azure.com/mode=system' --overwrite
az-untaintsystemnodes: ## Untaint all system nodepool nodes
	kubectl taint nodes CriticalAddonsOnly=true:NoSchedule- --selector='kubernetes.azure.com/mode=system' --overwrite

az-taintnodes:
	kubectl taint nodes CriticalAddonsOnly=true:NoSchedule --all --overwrite

az-e2etests: az-cleanenv ## Run e2etests
	kubectl taint nodes CriticalAddonsOnly=true:NoSchedule --all --overwrite
	AZURE_SUBSCRIPTION_ID=$(AZURE_SUBSCRIPTION_ID) \
	AZURE_CLUSTER_NAME=$(AZURE_CLUSTER_NAME) \
	AZURE_RESOURCE_GROUP=$(AZURE_RESOURCE_GROUP) \
	AZURE_ACR_NAME=$(AZURE_ACR_NAME) \
	make e2etests
	kubectl taint nodes CriticalAddonsOnly=true:NoSchedule- --all

az-upstream-e2etests: az-cleanenv ## Run upstream e2etests
	kubectl taint nodes CriticalAddonsOnly=true:NoSchedule --all --overwrite
	AZURE_SUBSCRIPTION_ID=$(AZURE_SUBSCRIPTION_ID) \
	AZURE_CLUSTER_NAME=$(AZURE_CLUSTER_NAME) \
	AZURE_RESOURCE_GROUP=$(AZURE_RESOURCE_GROUP) \
	AZURE_ACR_NAME=$(AZURE_ACR_NAME) \
	make upstream-e2etests
	kubectl taint nodes CriticalAddonsOnly=true:NoSchedule- --all

az-perftest1: ## Test scaling out/in (1 VM)
	hack/azure/perftest.sh 1

az-perftest5: ## Test scaling out/in (5 VMs)
	hack/azure/perftest.sh 5

az-perftest20: ## Test scaling out/in (20 VMs)
	hack/azure/perftest.sh 20

az-perftest100: ## Test scaling out/in (100 VMs)
	hack/azure/perftest.sh 100

az-perftest300: ## Test scaling out/in (300 VMs)
	hack/azure/perftest.sh 300

az-perftest400: ## Test scaling out/in (400 VMs)
	hack/azure/perftest.sh 400

az-perftest500: ## Test scaling out/in (500 VMs)
	hack/azure/perftest.sh 500

az-perftest1000: ## Test scaling out/in (1000 VMs)
	hack/azure/perftest.sh 1000

az-resg: ## List resources in MC rg
	az resource list -o table -g $(AZURE_RESOURCE_GROUP_MC)

RESK=az resource list --tag=karpenter.sh_nodepool --query "[?resourceGroup=='$(AZURE_RESOURCE_GROUP_MC)']"
az-res: ## List resources created by Karpenter
	$(RESK) -o table

az-resc: ## Count resources created by Karpenter
	$(RESK) -o tsv | wc -l

az-rmres: ## Delete (az resource delete) all resources created by Karpenter. Use with extra care!
	$(RESK) -o yaml | yq eval '.[]|.id' | xargs --verbose -r -n 5 az resource delete --ids

az-rmcluster:
	az aks delete --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --yes

az-portal: ## Get Azure Portal links for relevant resource groups
	@echo https://ms.portal.azure.com/#@microsoft.onmicrosoft.com/asset/HubsExtension/ResourceGroups/subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP)
	@echo https://ms.portal.azure.com/#@microsoft.onmicrosoft.com/asset/HubsExtension/ResourceGroups/subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP_MC)

az-list-skus-ubuntu:
	az sig image-definition list-community --public-gallery-name "AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2"  --location $(AZURE_LOCATION) -o table

az-list-skus-azlinux:
	az sig image-definition list-community --public-gallery-name "AKSAzureLinux-f7c7cda5-1c9a-4bdc-a222-9614c968580b"  --location $(AZURE_LOCATION) -o table

az-list-skus: ## List all public VM images from microsoft-aks
	az vm image list-skus --publisher microsoft-aks --location $(AZURE_LOCATION) --offer aks -o table

az-list-usage: ## List VM usage/quotas
	az vm list-usage --location $(AZURE_LOCATION) -o table | grep "Family vCPU"

az-ratelimits: ## Show remaining ARM requests for subscription
	@az group create --name $(AZURE_RESOURCE_GROUP) --location $(AZURE_LOCATION) --debug 2>&1 | grep x-ms-ratelimit-remaining-subscription-writes
	@az group show   --name $(AZURE_RESOURCE_GROUP)                              --debug 2>&1 | grep x-ms-ratelimit-remaining-subscription-reads

az-kdebug: ## Inject ephemeral debug container (kubectl debug) into Karpenter pod
	$(eval POD=$(shell kubectl get pods -l app.kubernetes.io/name=karpenter -n "${KARPENTER_NAMESPACE}" -o name))
	kubectl debug -n "${KARPENTER_NAMESPACE}" $(POD) --image wbitt/network-multitool -it -- sh

az-klogs-watch: ## Watch Karpenter logs
	$(eval POD=$(shell kubectl get pods -l app.kubernetes.io/name=karpenter -n "${KARPENTER_NAMESPACE}" -o name))
	kubectl logs -f -n "${KARPENTER_NAMESPACE}" $(POD)

az-klogs-pretty: ## Pretty Print Karpenter logs
	$(eval POD=$(shell kubectl get pods -l app.kubernetes.io/name=karpenter -n "${KARPENTER_NAMESPACE}" -o name))
	kubectl logs -n "${KARPENTER_NAMESPACE}" $(POD) | jq "."

az-kevents: ## Karpenter events
	kubectl get events -A --field-selector source=karpenter --watch

az-node-viewer: ## Watch nodes using aks-node-viewer
	aks-node-viewer # --node-selector "karpenter.sh/nodepool" --resources cpu,memory

az-argvmlist: ## List current VMs owned by Karpenter
	az graph query -q "Resources | where type =~ 'microsoft.compute/virtualmachines' | where resourceGroup == tolower('$(AZURE_RESOURCE_GROUP_MC)') | where tags has_cs 'karpenter.sh_nodepool'" \
	--subscriptions $(AZURE_SUBSCRIPTION_ID) \
	| jq '.data[] | .id'

az-pprof-enable: ## Enable profiling
	yq -i '.controller.env += [{"name":"ENABLE_PROFILING","value":"true"}]' karpenter-values.yaml

# remove -source_path=/go/pkg/mod to focus source on provider
SOURCE=-source_path=/go/pkg/mod -trim_path=github.com/Azure/karpenter-provider-azure
az-pprof: ## Profile
	kubectl port-forward service/karpenter -n "${KARPENTER_NAMESPACE}" 8080 &
	sleep 2
	go tool pprof $(SOURCE) -http 0.0.0.0:9000 localhost:8080/debug/pprof/heap
	#go tool pprof $(SOURCE) -http 0.0.0.0:9001 localhost:8080/debug/pprof/profile?seconds=30

az-mkaks-user: az-mkrg ## Create compatible AKS cluster, the way we tell users to
	hack/deploy/create-cluster.sh $(AZURE_CLUSTER_NAME) $(AZURE_RESOURCE_GROUP) "${KARPENTER_NAMESPACE}"

az-helm-install-snapshot: az-configure-values ## Install Karpenter snapshot release
	$(eval SNAPSHOT_VERSION ?= $(shell git rev-parse HEAD)) # guess which, specify explicitly with SNAPSHOT_VERSION=...
	helm upgrade --install karpenter oci://ksnap.azurecr.io/karpenter/snapshot/karpenter \
		--version 0-$(SNAPSHOT_VERSION) \
		--namespace "${KARPENTER_NAMESPACE}" --create-namespace \
		--values karpenter-values.yaml \
		--set controller.resources.requests.cpu=1 \
		--set controller.resources.requests.memory=1Gi \
		--set controller.resources.limits.cpu=1 \
		--set controller.resources.limits.memory=1Gi \
		--wait

az-rmcrds: ## Delete Karpenter CRDs
	kubectl delete crd nodepools.karpenter.sh nodeclaims.karpenter.sh aksnodeclasses.karpenter.azure.com

az-swagger-generate-clients-raw:
	cd pkg/provisionclients && swagger generate client -f swagger/*.json
	hack/azure/temp_fix_get_bootstrapping_resp_error.sh

az-swagger-generate-clients: az-swagger-generate-clients-raw
	hack/boilerplate.sh
	make tidy

az-codegen-nodeimageversions: ## List node image versions (to be used in fake/nodeimageversionsapi.go)
	az rest --method get \
		--url "/subscriptions/$(AZURE_SUBSCRIPTION_ID)/providers/Microsoft.ContainerService/locations/$(AZURE_LOCATION)/nodeImageVersions?api-version=2024-04-02-preview" \
		| jq -r '.values[] | "{\n\tFullName: \"\(.fullName)\",\n\tOS:       \"\(.os)\",\n\tSKU:      \"\(.sku)\",\n\tVersion:  \"\(.version)\",\n},"'

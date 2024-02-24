AZURE_LOCATION ?= westus2
ifeq ($(CODESPACES),true)
  AZURE_RESOURCE_GROUP ?= $(CODESPACE_NAME)
  AZURE_ACR_NAME ?= $(subst -,,$(CODESPACE_NAME))
else
  AZURE_RESOURCE_GROUP ?= karpeeer 
  AZURE_ACR_NAME ?= karpenter
endif


AZURE_CLUSTER_NAME ?= karpeeer
AZURE_RESOURCE_GROUP_MC = MC_$(AZURE_RESOURCE_GROUP)_$(AZURE_CLUSTER_NAME)_$(AZURE_LOCATION)

KARPENTER_SERVICE_ACCOUNT_NAME ?= karpenter-sa
AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME ?= karpentermsi
KARPENTER_FEDERATED_IDENTITY_CREDENTIAL_NAME ?= KARPENTER_FID


az-all:         az-login az-create-workload-msi az-mkaks-cilium az-create-federated-cred az-perm az-perm-acr az-patch-skaffold-azureoverlay az-build az-run az-run-sample ## Provision the infra (ACR,AKS); build and deploy Karpenter; deploy sample Provisioner and workload
az-all-savm:    az-login az-mkaks-savm az-perm-savm az-patch-skaffold-azure az-build az-run az-run-sample ## Provision the infra (ACR,AKS); build and deploy Karpenter; deploy sample Provisioner and workload - StandaloneVirtualMachines

az-login: ## Login into Azure
	az login
	az account set --subscription $(AZURE_SUBSCRIPTION_ID)

az-mkrg: ## Create resource group
	if az group exists --name $(AZURE_RESOURCE_GROUP) | grep -q "false"; then \
		az group create --name $(AZURE_RESOURCE_GROUP) --location $(AZURE_LOCATION) -o none; \
	fi

az-mkacr: az-mkrg ## Create test ACR
	az acr create --name $(AZURE_ACR_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --sku Basic --admin-enabled -o none
	az acr login  --name $(AZURE_ACR_NAME)

az-mkaks: az-mkacr ## Create test AKS cluster (with --vm-set-type AvailabilitySet for compatibility with standalone VMs)
	az aks create          --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --attach-acr $(AZURE_ACR_NAME) --location $(AZURE_LOCATION) \
		--enable-managed-identity --node-count 3 --generate-ssh-keys --vm-set-type AvailabilitySet -o none
	az aks get-credentials --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --overwrite-existing
	skaffold config set default-repo $(AZURE_ACR_NAME).azurecr.io/karpenter

az-mkaks-cilium-nap:
	az extension add --name aks-preview
	az feature register --namespace "Microsoft.ContainerService" --name "NodeAutoProvisioningPreview"
	az aks create          --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP)  \
		--enable-managed-identity --node-count 3 --generate-ssh-keys -o none --network-dataplane cilium --network-plugin azure --network-plugin-mode overlay \
		--enable-oidc-issuer --enable-managed-identity --node-provisioning-mode Auto

	az aks get-credentials --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --overwrite-existing

az-mkaks-cilium: az-mkacr ## Create test AKS cluster (with --network-dataplane cilium, --network-plugin cilium, and --network-plugin-mode overlay)
	az aks create          --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --attach-acr $(AZURE_ACR_NAME) \
		--enable-managed-identity --node-count 3 --generate-ssh-keys -o none --network-dataplane cilium --network-plugin azure --network-plugin-mode overlay \
		--enable-oidc-issuer --enable-workload-identity
	az aks get-credentials --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --overwrite-existing
	skaffold config set default-repo $(AZURE_ACR_NAME).azurecr.io/karpenter

az-create-workload-msi: az-mkrg
	# create the workload MSI that is the backing for the karpenter pod auth
	az identity create --name "${AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME}" --resource-group "${AZURE_RESOURCE_GROUP}" --location "${AZURE_LOCATION}"

az-create-federated-cred:
	$(eval AKS_OIDC_ISSUER=$(shell az aks show -n "${AZURE_CLUSTER_NAME}" -g "${AZURE_RESOURCE_GROUP}" --query "oidcIssuerProfile.issuerUrl" -otsv))

	# create federated credential linked to the karpenter service account for auth usage
	az identity federated-credential create --name ${KARPENTER_FEDERATED_IDENTITY_CREDENTIAL_NAME} --identity-name "${AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME}" --resource-group "${AZURE_RESOURCE_GROUP}" --issuer "${AKS_OIDC_ISSUER}" --subject system:serviceaccount:"${SYSTEM_NAMESPACE}":"${KARPENTER_SERVICE_ACCOUNT_NAME}" --audience api://AzureADTokenExchange

az-mkaks-savm: az-mkrg ## Create experimental cluster with standalone VMs (+ ACR)
	az deployment group create --resource-group $(AZURE_RESOURCE_GROUP) --template-file hack/azure/aks-savm.bicep --parameters aksname=$(AZURE_CLUSTER_NAME) acrname=$(AZURE_ACR_NAME)
	az aks get-credentials --resource-group $(AZURE_RESOURCE_GROUP) --name $(AZURE_CLUSTER_NAME) --overwrite-existing
	skaffold config set default-repo $(AZURE_ACR_NAME).azurecr.io/karpenter

az-rmrg: ## Destroy test ACR and AKS cluster by deleting the resource group (use with care!)
	az group delete --name $(AZURE_RESOURCE_GROUP)

az-patch-skaffold: 	## Update Azure client env vars and settings in skaffold config
	$(eval AZURE_CLIENT_ID=$(shell az aks show --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) | jq -r ".identityProfile.kubeletidentity.clientId"))
	$(eval CLUSTER_ENDPOINT=$(shell kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}'))
	# bootstrap token
	$(eval TOKEN_SECRET_NAME=$(shell kubectl get -n kube-system secrets --field-selector=type=bootstrap.kubernetes.io/token -o jsonpath='{.items[0].metadata.name}'))
	$(eval TOKEN_ID=$(shell          kubectl get -n kube-system secret $(TOKEN_SECRET_NAME) -o jsonpath='{.data.token-id}'     | base64 -d))
	$(eval TOKEN_SECRET=$(shell      kubectl get -n kube-system secret $(TOKEN_SECRET_NAME) -o jsonpath='{.data.token-secret}' | base64 -d))
	$(eval BOOTSTRAP_TOKEN=$(TOKEN_ID).$(TOKEN_SECRET))
	# ssh key
	$(eval SSH_PUBLIC_KEY=$(shell cat ~/.ssh/id_rsa.pub) azureuser)
	yq -i '(.manifests.helm.releases[0].overrides.controller.env[] | select(.name=="ARM_SUBSCRIPTION_ID"))           .value = "$(AZURE_SUBSCRIPTION_ID)"'   skaffold.yaml
	yq -i '(.manifests.helm.releases[0].overrides.controller.env[] | select(.name=="LOCATION"))                      .value = "$(AZURE_LOCATION)"'          skaffold.yaml
	yq -i '(.manifests.helm.releases[0].overrides.controller.env[] | select(.name=="ARM_USER_ASSIGNED_IDENTITY_ID")) .value = "$(AZURE_CLIENT_ID)"'         skaffold.yaml
	yq -i '(.manifests.helm.releases[0].overrides.controller.env[] | select(.name=="AZURE_NODE_RESOURCE_GROUP"))     .value = "$(AZURE_RESOURCE_GROUP_MC)"' skaffold.yaml
	yq -i  '(.manifests.helm.releases[0].overrides.controller.env[] | select(.name=="CLUSTER_NAME")).value =                                                "$(AZURE_CLUSTER_NAME)"'      skaffold.yaml
	yq -i  '(.manifests.helm.releases[0].overrides.controller.env[] | select(.name=="CLUSTER_ENDPOINT")).value =                                            "$(CLUSTER_ENDPOINT)"'        skaffold.yaml
	yq -i  '(.manifests.helm.releases[0].overrides.controller.env[] | select(.name=="NETWORK_PLUGIN")).value =                                              "azure"'                      skaffold.yaml
	yq -i  '(.manifests.helm.releases[0].overrides.controller.env[] | select(.name=="KUBELET_BOOTSTRAP_TOKEN")).value =                             "$(BOOTSTRAP_TOKEN)"'         skaffold.yaml
	yq -i  '(.manifests.helm.releases[0].overrides.controller.env[] | select(.name=="SSH_PUBLIC_KEY")).value =                                               "$(SSH_PUBLIC_KEY)"'          skaffold.yaml

az-patch-skaffold-kubenet: az-patch-skaffold	az-fetch-network-info
	$(eval AZURE_SUBNET_ID=$(shell az network vnet list --resource-group $(AZURE_RESOURCE_GROUP_MC) | jq  -r ".[0].subnets[0].id"))
	yq -i '(.manifests.helm.releases[0].overrides.controller.env[] | select(.name=="AZURE_SUBNET_ID"))               .value = "$(AZURE_SUBNET_ID)"'         skaffold.yaml
	yq -i  '(.manifests.helm.releases[0].overrides.controller.env[] | select(.name=="NETWORK_PLUGIN").value) =                                              "kubenet"'                    skaffold.yaml

az-patch-skaffold-azure: az-patch-skaffold	az-fetch-network-info
	$(eval AZURE_SUBNET_ID=$(shell az aks show --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) | jq -r ".agentPoolProfiles[0].vnetSubnetId"))
	yq -i '(.manifests.helm.releases[0].overrides.controller.env[] | select(.name=="AZURE_SUBNET_ID"))               .value = "$(AZURE_SUBNET_ID)"'         skaffold.yaml

az-patch-skaffold-azureoverlay: az-patch-skaffold	az-fetch-network-info
	$(eval AZURE_SUBNET_ID=$(shell az network vnet list --resource-group $(AZURE_RESOURCE_GROUP_MC) | jq  -r ".[0].subnets[0].id"))
	yq -i '(.manifests.helm.releases[0].overrides.controller.env[] | select(.name=="AZURE_SUBNET_ID")) .value = "$(AZURE_SUBNET_ID)"' skaffold.yaml
	yq -i  '(.manifests.helm.releases[0].overrides.controller.env[] | select(.name=="NETWORK_PLUGIN").value) =                                              "azure"'                      skaffold.yaml

	# old identity path is still the default, so need to override the values values with new logic.
	# TODO (chmcbrid): update the new logic path as the default.
	$(eval KARPENTER_USER_ASSIGNED_CLIENT_ID=$(shell az identity show --resource-group "${AZURE_RESOURCE_GROUP}" --name "${AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME}" --query 'clientId' -otsv))
	yq -i '(.manifests.helm.releases[0].overrides.controller.env[] | select(.name=="ARM_USE_CREDENTIAL_FROM_ENVIRONMENT")) .value = "true"'         skaffold.yaml
	yq -i '(.manifests.helm.releases[0].overrides.controller.env[] | select(.name=="ARM_USE_MANAGED_IDENTITY_EXTENSION")) .value = "false"'         skaffold.yaml
	yq -i '(.manifests.helm.releases[0].overrides.controller.env[] | select(.name=="ARM_USER_ASSIGNED_IDENTITY_ID")) .value = ""'         skaffold.yaml

	yq -i  '.manifests.helm.releases[0].overrides.serviceAccount.annotations."azure.workload.identity/client-id" = "$(KARPENTER_USER_ASSIGNED_CLIENT_ID)"' skaffold.yaml
	yq -i  '.manifests.helm.releases[0].overrides.serviceAccount.name = "$(KARPENTER_SERVICE_ACCOUNT_NAME)"' skaffold.yaml

	yq -i  '.manifests.helm.releases[0].overrides.podLabels ."azure.workload.identity/use" = "true"' skaffold.yaml

az-fetch-network-info:
	$(eval AZURE_VNET_NAME=$(shell az network vnet list --resource-group $(AZURE_RESOURCE_GROUP_MC) |  jq -r ".[0].name"))
	yq -i '(.manifests.helm.releases[0].overrides.controller.env[] | select(.name=="AZURE_VNET_NAME"))	.value = "$(AZURE_VNET_NAME)"'         skaffold.yaml
	$(eval AZURE_SUBNET_NAME=$(shell az network vnet list --resource-group $(AZURE_RESOURCE_GROUP_MC) |  jq -r ".[0].subnets[0].name"))
	yq -i '(.manifests.helm.releases[0].overrides.controller.env[] | select(.name=="AZURE_SUBNET_NAME"))	.value = "$(AZURE_SUBNET_NAME)"'         skaffold.yaml

az-mkvmssflex: ## Create VMSS Flex (optional, only if creating VMs referencing this VMSS)
	az vmss create --name $(AZURE_CLUSTER_NAME)-vmss --resource-group $(AZURE_RESOURCE_GROUP_MC) --location $(AZURE_LOCATION) \
		--instance-count 0 --orchestration-mode Flexible --platform-fault-domain-count 1 --zones 1 2 3

az-rmvmss-vms: ## Delete all VMs in VMSS Flex (use with care!)
	az vmss delete-instances --name $(AZURE_CLUSTER_NAME)-vmss --resource-group $(AZURE_RESOURCE_GROUP_MC) --instance-ids '*'

az-perm: ## Create role assignments to let Karpenter manage VMs and Network
	# Note: need to be principalId for E2E workflow as the pipeline identity doesn't have permissions to "query Graph API"
	$(eval KARPENTER_USER_ASSIGNED_CLIENT_ID=$(shell az identity show --resource-group "${AZURE_RESOURCE_GROUP}" --name "${AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME}" --query 'principalId' -otsv))
	az role assignment create --assignee $(KARPENTER_USER_ASSIGNED_CLIENT_ID) --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP_MC) --role "Virtual Machine Contributor"
	az role assignment create --assignee $(KARPENTER_USER_ASSIGNED_CLIENT_ID) --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP_MC) --role "Network Contributor"
	az role assignment create --assignee $(KARPENTER_USER_ASSIGNED_CLIENT_ID) --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP_MC) --role "Managed Identity Operator"
	az role assignment create --assignee $(KARPENTER_USER_ASSIGNED_CLIENT_ID) --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP)    --role "Network Contributor" # in some case we create vnet here
	@echo Consider "make az-patch-skaffold"!

az-perm-savm: ## Create role assignments to let Karpenter manage VMs and Network
	# Note: savm has not been converted over to use a workload identity
	$(eval AZURE_OBJECT_ID=$(shell az aks show --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) | jq  -r ".identityProfile.kubeletidentity.objectId"))
	az role assignment create --assignee $(AZURE_OBJECT_ID) --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP_MC) --role "Virtual Machine Contributor"
	az role assignment create --assignee $(AZURE_OBJECT_ID) --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP_MC) --role "Network Contributor"
	az role assignment create --assignee $(AZURE_OBJECT_ID) --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP_MC) --role "Managed Identity Operator"
	az role assignment create --assignee $(AZURE_OBJECT_ID) --scope /subscriptions/$(AZURE_SUBSCRIPTION_ID)/resourceGroups/$(AZURE_RESOURCE_GROUP)    --role "Network Contributor" # in some case we create vnet here
	@echo Consider "make az-patch-skaffold"!

az-perm-acr:
	$(eval KARPENTER_USER_ASSIGNED_CLIENT_ID=$(shell az identity show --resource-group "${AZURE_RESOURCE_GROUP}" --name "${AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME}" --query 'principalId' -otsv))
	$(eval AZURE_ACR_ID=$(shell    az acr show --name $(AZURE_ACR_NAME)     --resource-group $(AZURE_RESOURCE_GROUP) | jq  -r ".id"))
	az role assignment create --assignee $(KARPENTER_USER_ASSIGNED_CLIENT_ID) --scope $(AZURE_ACR_ID) --role "AcrPull"

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
	kubectl apply -f examples/v1beta1/general-purpose.yaml
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

az-mon-deploy: ## Deploy monitoring stack (w/o node-exporter)
	helm repo add grafana-charts https://grafana.github.io/helm-charts
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
	helm repo update
	kubectl create namespace monitoring || true
	helm install --namespace monitoring prometheus prometheus-community/prometheus \
		--values examples/prometheus-values.yaml \
		--set nodeExporter.enabled=false
	helm install --namespace monitoring grafana grafana-charts/grafana \
		--values examples/grafana-values.yaml

az-mon-access: ## Get Grafana admin password and forward port
	kubectl get secret --namespace monitoring grafana -o jsonpath="{.data.admin-password}" | base64 --decode; echo
	@echo Consider running port forward outside of codespace ...
	kubectl port-forward --namespace monitoring svc/grafana 3000:80

az-mon-cleanup: ## Delete monitoring stack
	helm delete --namespace monitoring grafana
	helm delete --namespace monitoring prometheus

az-mkgohelper: ## Build and configure custom go-helper-image for skaffold
	cd hack/go-helper-image; docker build . --tag $(AZURE_ACR_NAME).azurecr.io/skaffold-debug-support/go # --platform=linux/arm64
	az acr login -n $(AZURE_ACR_NAME)
	docker push $(AZURE_ACR_NAME).azurecr.io/skaffold-debug-support/go
	skaffold config set --global debug-helpers-registry $(AZURE_ACR_NAME).azurecr.io/skaffold-debug-support

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

az-rmnodeclaims: ## kubectl delete all nodeclaims; don't wait for finalizers (use with care!)
	kubectl delete --wait=false nodeclaims --all

az-e2etests: ## Run e2etests
	kubectl taint nodes CriticalAddonsOnly=true:NoSchedule --all --overwrite
	TEST_SUITE=Utilization make e2etests
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

az-list-skus: ## List all public VM images from microsoft-aks
	az vm image list-skus --publisher microsoft-aks --location $(AZURE_LOCATION) --offer aks -o table

az-list-usage: ## List VM usage/quotas
	az vm list-usage --location $(AZURE_LOCATION) -o table | grep "Family vCPU"

az-ratelimits: ## Show remaining ARM requests for subscription
	@az group create --name $(AZURE_RESOURCE_GROUP) --location $(AZURE_LOCATION) --debug 2>&1 | grep x-ms-ratelimit-remaining-subscription-writes
	@az group show   --name $(AZURE_RESOURCE_GROUP)                              --debug 2>&1 | grep x-ms-ratelimit-remaining-subscription-reads

az-kdebug: ## Inject ephemeral debug container (kubectl debug) into Karpenter pod
	$(eval POD=$(shell kubectl get pods -l app.kubernetes.io/name=karpenter -n karpenter -o name))
	kubectl debug -n karpenter $(POD) --image wbitt/network-multitool -it -- sh

az-klogs: ## Karpenter logs
	$(eval POD=$(shell kubectl get pods -l app.kubernetes.io/name=karpenter -n karpenter -o name))
	kubectl logs -f -n karpenter $(POD)

az-kevents: ## Karpenter events
	kubectl get events -A --field-selector source=karpenter

az-node-viewer: ## Watch nodes using eks-node-viewer
	eks-node-viewer --disable-pricing --node-selector "karpenter.sh/nodepool" # --resources cpu,memory

az-argvmlist: ## List current VMs owned by Karpenter
	az graph query -q "Resources | where type =~ 'microsoft.compute/virtualmachines' | where resourceGroup == tolower('$(AZURE_RESOURCE_GROUP_MC)') | where tags has_cs 'karpenter.sh_nodepool'" \
	--subscriptions $(AZURE_SUBSCRIPTION_ID) \
	| jq '.data[] | .id'

az-pprof-enable: ## Enable profiling
	yq -i '.manifests.helm.releases[0].overrides.controller.env += [{"name":"ENABLE_PROFILING","value":"true"}]' skaffold.yaml

az-pprof: ## Profile
	kubectl port-forward service/karpenter -n karpenter 8000 &
	sleep 2 && go tool pprof -http 0.0.0.0:9000 localhost:8000/debug/pprof/heap

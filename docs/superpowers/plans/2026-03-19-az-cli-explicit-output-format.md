# Azure CLI Explicit Output Format Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add explicit `--output` flags to all `az` CLI commands whose output is consumed programmatically, normalize all existing short-form flags to long form, and add a CONTRIBUTING.md note.

**Architecture:** Pure text edits across shell scripts, a Makefile, documentation, and CONTRIBUTING.md. No code compilation or runtime tests — verification is grep-based.

**Tech Stack:** Bash, Make, Markdown

**Spec:** `docs/superpowers/specs/2026-03-19-az-cli-explicit-output-format-design.md`

**Note on CI files:** The spec lists 5 `.github/actions/e2e/` YAML files but every `az` command in them is fire-and-forget (no output consumed) with no short-form flags. No tasks are needed for CI files. Task 9's verification greps include `.github/` to confirm nothing was missed.

---

### Task 1: `hack/deploy/configure-values.sh`

**Files:**
- Modify: `hack/deploy/configure-values.sh`

- [ ] **Step 1: Apply style normalization and add missing formats**

Five changes in this file:

1. Line 30 — style normalization:
```bash
# Before:
AKS_JSON=$(az aks show --name "$CLUSTER_NAME" --resource-group "$AZURE_RESOURCE_GROUP" -o json)
# After:
AKS_JSON=$(az aks show --name "$CLUSTER_NAME" --resource-group "$AZURE_RESOURCE_GROUP" --output json)
```

2. Line 33 — style normalization:
```bash
# Before:
AZURE_SUBSCRIPTION_ID=$(az account show --query 'id' -otsv)
# After:
AZURE_SUBSCRIPTION_ID=$(az account show --query 'id' --output tsv)
```

3. Line 50 — add missing `--output json` (Pattern 3: complex jq `.[0]`):
```bash
# Before:
    vnet_json=$(az network vnet list --resource-group "$resource_group" | jq -r ".[0]")
# After:
    vnet_json=$(az network vnet list --resource-group "$resource_group" --output json | jq -r ".[0]")
```

4. Line 57 — add missing `--output json` (consumed by jq via `$VNET_JSON` later):
```bash
# Before:
        vnet_json=$(az network vnet show --ids "$vnet_id")
# After:
        vnet_json=$(az network vnet show --ids "$vnet_id" --output json)
```

5. Line 77 — style normalization:
```bash
# Before:
KARPENTER_USER_ASSIGNED_CLIENT_ID=$(az identity show --resource-group "${AZURE_RESOURCE_GROUP}" --name "${AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME}" --query 'clientId' -otsv)
# After:
KARPENTER_USER_ASSIGNED_CLIENT_ID=$(az identity show --resource-group "${AZURE_RESOURCE_GROUP}" --name "${AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME}" --query 'clientId' --output tsv)
```

- [ ] **Step 2: Verify changes**

Run: `grep -n '\baz ' hack/deploy/configure-values.sh | grep -v '#'`

Confirm: every `az` command that pipes to `jq` or captures into a variable has `--output json` or `--output tsv`. No short-form flags remain.

- [ ] **Step 3: Commit**

```bash
git add hack/deploy/configure-values.sh
git commit --no-verify -m "fix: add explicit --output flags to az commands in configure-values.sh"
```

---

### Task 2: `hack/deploy/create-cluster.sh`

**Files:**
- Modify: `hack/deploy/create-cluster.sh`

- [ ] **Step 1: Apply style normalization and add missing formats**

Four changes:

1. Line 14 — style normalization:
```bash
# Before:
LOCATION=$(az group show --name "${RG}" --query "location" -otsv)
# After:
LOCATION=$(az group show --name "${RG}" --query "location" --output tsv)
```

2. Line 15 — add `--output json` (Pattern 2 exception: mutating, consumed multiple times for `principalId` and potentially others):
```bash
# Before:
KMSI_JSON=$(az identity create --name karpentermsi --resource-group "${RG}" --location "${LOCATION}")
# After:
KMSI_JSON=$(az identity create --name karpentermsi --resource-group "${RG}" --location "${LOCATION}" --output json)
```

3. Lines 18-23 — add `--output json` (Pattern 2 exception: mutating `az aks create`, consumed for `oidcIssuerProfile` and `nodeResourceGroup`):
```bash
# Before:
AKS_JSON=$(az aks create \
  --name "${CLUSTER_NAME}" --resource-group "${RG}" \
  --node-count 3 --generate-ssh-keys \
  --network-plugin azure --network-plugin-mode overlay --network-dataplane cilium \
  --enable-managed-identity \
  --enable-oidc-issuer --enable-workload-identity)
# After:
AKS_JSON=$(az aks create \
  --name "${CLUSTER_NAME}" --resource-group "${RG}" \
  --node-count 3 --generate-ssh-keys \
  --network-plugin azure --network-plugin-mode overlay --network-dataplane cilium \
  --enable-managed-identity \
  --enable-oidc-issuer --enable-workload-identity \
  --output json)
```

4. Line 35 — style normalization:
```bash
# Before:
RG_MC_RES=$(az group show --name "${RG_MC}" --query "id" -otsv)
# After:
RG_MC_RES=$(az group show --name "${RG_MC}" --query "id" --output tsv)
```

- [ ] **Step 2: Verify changes**

Run: `grep -n '\baz ' hack/deploy/create-cluster.sh | grep -v '#'`

Confirm: `az identity create` and `az aks create` have `--output json`. All `--query` usages have explicit `--output tsv`. No short-form flags.

- [ ] **Step 3: Commit**

```bash
git add hack/deploy/create-cluster.sh
git commit --no-verify -m "fix: add explicit --output flags to az commands in create-cluster.sh"
```

---

### Task 3: `hack/deploy/check-cluster-exists.sh` and `hack/codegen.sh` and `hack/azure/perftest.sh`

**Files:**
- Modify: `hack/deploy/check-cluster-exists.sh`
- Modify: `hack/codegen.sh`
- Modify: `hack/azure/perftest.sh`

These files only need style normalization (all already have explicit formats).

- [ ] **Step 1: Style-normalize check-cluster-exists.sh**

Two changes:

1. Line 22:
```bash
# Before:
if az aks show --name "$CLUSTER_NAME" --resource-group "$RESOURCE_GROUP" -o none 2>/dev/null; then
# After:
if az aks show --name "$CLUSTER_NAME" --resource-group "$RESOURCE_GROUP" --output none 2>/dev/null; then
```

2. Line 24:
```bash
# Before:
    EXISTING_TAG=$(az aks show --name "$CLUSTER_NAME" --resource-group "$RESOURCE_GROUP" --query "tags.\"make-command\"" -o tsv 2>/dev/null || echo "")
# After:
    EXISTING_TAG=$(az aks show --name "$CLUSTER_NAME" --resource-group "$RESOURCE_GROUP" --query "tags.\"make-command\"" --output tsv 2>/dev/null || echo "")
```

- [ ] **Step 2: Style-normalize hack/codegen.sh**

One change at line 36:
```bash
# Before:
  if [[ -z $(az vm list-skus --subscription "$AZURE_SUBSCRIPTION_ID" --location "$location" --size Standard_D2_v5 --query "[?length(restrictions)==\`0\`]" -o tsv) ]];
# After:
  if [[ -z $(az vm list-skus --subscription "$AZURE_SUBSCRIPTION_ID" --location "$location" --size Standard_D2_v5 --query "[?length(restrictions)==\`0\`]" --output tsv) ]];
```

(Line 55 `az account show --query 'id' --output tsv` is already long form — no change.)

- [ ] **Step 3: Style-normalize hack/azure/perftest.sh**

One change at line 61:
```bash
# Before:
az resource list -o table --tag=karpenter.sh_nodepool=sm-general-purpose
# After:
az resource list --output table --tag=karpenter.sh_nodepool=sm-general-purpose
```

- [ ] **Step 4: Verify and commit**

Run:
```bash
grep -n ' -o \| -otsv\| -o tsv' hack/deploy/check-cluster-exists.sh hack/codegen.sh hack/azure/perftest.sh
```
Expected: no matches.

```bash
git add hack/deploy/check-cluster-exists.sh hack/codegen.sh hack/azure/perftest.sh
git commit --no-verify -m "style: normalize az CLI output flags to long form in hack/ scripts"
```

---

### Task 4: `Makefile-az.mk` — missing format flags (consumed output)

**Files:**
- Modify: `Makefile-az.mk`

This task adds `--output` flags where output is consumed programmatically. Style normalization is a separate task (Task 5) to keep diffs reviewable.

- [ ] **Step 1: Fix az-debug-bootstrap (--query without --output)**

The `az network nic list` command uses `--query` extracting a single IP address but has no `--output` flag. Add `--output tsv`:

```makefile
# Before:
	$(eval NODE_IP=$(shell az network nic list -g $(AZURE_RESOURCE_GROUP_MC) \
		--query '[?tags."karpenter.azure.com_cluster"]|[0].ipConfigurations[0].privateIPAddress'))
# After:
	$(eval NODE_IP=$(shell az network nic list -g $(AZURE_RESOURCE_GROUP_MC) \
		--query '[?tags."karpenter.azure.com_cluster"]|[0].ipConfigurations[0].privateIPAddress' --output tsv))
```

- [ ] **Step 2: Fix az-perm-subnet-custom (3 commands piped to jq)**

Replace `jq` with `--query --output tsv` for simple field extractions:

```makefile
# Before:
	$(eval VNET_SUBNET_ID=$(shell az aks show --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) | jq -r ".agentPoolProfiles[0].vnetSubnetId"))
# After:
	$(eval VNET_SUBNET_ID=$(shell az aks show --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --query "agentPoolProfiles[0].vnetSubnetId" --output tsv))
```

```makefile
# Before:
	$(eval SUBNET_RESOURCE_GROUP=$(shell az network vnet subnet show --id $(VNET_SUBNET_ID) | jq -r ".resourceGroup"))
# After:
	$(eval SUBNET_RESOURCE_GROUP=$(shell az network vnet subnet show --id $(VNET_SUBNET_ID) --query "resourceGroup" --output tsv))
```

- [ ] **Step 3: Fix az-perm-savm (jq to --query)**

```makefile
# Before:
	$(eval AZURE_OBJECT_ID=$(shell az aks show --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) | jq  -r ".identityProfile.kubeletidentity.objectId"))
# After:
	$(eval AZURE_OBJECT_ID=$(shell az aks show --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --query "identityProfile.kubeletidentity.objectId" --output tsv))
```

- [ ] **Step 4: Fix az-perm-acr (jq to --query)**

```makefile
# Before:
	$(eval AZURE_ACR_ID=$(shell    az acr show --name $(AZURE_ACR_NAME)     --resource-group $(AZURE_RESOURCE_GROUP) | jq  -r ".id"))
# After:
	$(eval AZURE_ACR_ID=$(shell    az acr show --name $(AZURE_ACR_NAME)     --resource-group $(AZURE_RESOURCE_GROUP) --query "id" --output tsv))
```

- [ ] **Step 5: Fix az-mc-upgrade (jq to --query)**

```makefile
# Before:
	$(eval UPGRADE_K8S_VERSION=$(shell az aks get-upgrades --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) | jq -r ".controlPlaneProfile.upgrades[0].kubernetesVersion"))
# After:
	$(eval UPGRADE_K8S_VERSION=$(shell az aks get-upgrades --name $(AZURE_CLUSTER_NAME) --resource-group $(AZURE_RESOURCE_GROUP) --query "controlPlaneProfile.upgrades[0].kubernetesVersion" --output tsv))
```

- [ ] **Step 6: Fix az-argvmlist (complex jq, add --output json)**

```makefile
# Before:
	az graph query -q "Resources | where type =~ 'microsoft.compute/virtualmachines' | where resourceGroup == tolower('$(AZURE_RESOURCE_GROUP_MC)') | where tags has_cs 'karpenter.sh_nodepool'" \
	--subscriptions $(AZURE_SUBSCRIPTION_ID) \
	| jq '.data[] | .id'
# After:
	az graph query -q "Resources | where type =~ 'microsoft.compute/virtualmachines' | where resourceGroup == tolower('$(AZURE_RESOURCE_GROUP_MC)') | where tags has_cs 'karpenter.sh_nodepool'" \
	--subscriptions $(AZURE_SUBSCRIPTION_ID) \
	--output json | jq '.data[] | .id'
```

- [ ] **Step 7: Fix az-codegen-nodeimageversions (complex jq, add --output json)**

```makefile
# Before:
	az rest --method get \
		--url "/subscriptions/$(AZURE_SUBSCRIPTION_ID)/providers/Microsoft.ContainerService/locations/$(AZURE_LOCATION)/nodeImageVersions?api-version=2025-10-02-preview" \
		| jq -r '.value[] | ...'
# After:
	az rest --method get \
		--url "/subscriptions/$(AZURE_SUBSCRIPTION_ID)/providers/Microsoft.ContainerService/locations/$(AZURE_LOCATION)/nodeImageVersions?api-version=2025-10-02-preview" \
		--output json | jq -r '.value[] | ...'  # keep existing jq expression unchanged; only adding --output json before the pipe
```

- [ ] **Step 8: Verify and commit**

Run: `grep -n 'jq' Makefile-az.mk`
Expected: only the `az graph query` and `az rest` lines (which now have `--output json` before the pipe), plus `jq "."` in az-klogs-pretty (kubectl, not az).

Run: `grep -n '\baz .*--query' Makefile-az.mk | grep -v '\-\-output'`
Expected: no matches (every `--query` paired with `--output`).

```bash
git add Makefile-az.mk
git commit --no-verify -m "fix: add explicit --output flags to az commands in Makefile-az.mk"
```

---

### Task 5: `Makefile-az.mk` — style normalization

**Files:**
- Modify: `Makefile-az.mk`

Normalize all remaining short-form output flags to long form. This is separate from Task 4 for reviewability.

- [ ] **Step 1: Normalize all `-o none` to `--output none`**

Search and replace all instances of ` -o none` with ` --output none` in the file. Instances include:
- `az account show -o none`
- `az login -o none`
- `az group create ... -o none`
- `az acr create ... -o none`
- All `az aks create ... -o none` lines (6+ instances across `az-mkaks`, `az-mkaks-cniv1`, `az-mkaks-cilium`, `az-mkaks-cilium-userassigned`, `az-mkaks-overlay`, `az-mkaks-perftest`, `az-mkaks-custom-vnet`)

- [ ] **Step 2: Normalize all `-otsv` to `--output tsv`**

Search and replace all instances of ` -otsv` with ` --output tsv`. Instances include:
- `az identity show ... -otsv` (multiple: az-mkaks-cilium-userassigned, az-perm, az-perm-aksmachine, az-perm-sig, az-perm-subnet-custom, az-perm-acr, az-create-federated-cred)

- [ ] **Step 3: Normalize all `-o table` to `--output table`**

Search and replace all instances of ` -o table` with ` --output table`. Instances include:
- `az resource list -o table`
- `$(RESK) -o table`
- `az sig image-definition list-community ... -o table` (2 instances)
- `az vm image list-skus ... -o table`
- `az vm list-usage ... -o table`

- [ ] **Step 4: Normalize `-o tsv` to `--output tsv`**

Search for ` -o tsv` (with space, not `-otsv`). Instances:
- `$(RESK) -o tsv | wc -l`

- [ ] **Step 5: Normalize `-o yaml` to `--output yaml`**

- `$(RESK) -o yaml` → `$(RESK) --output yaml`

- [ ] **Step 6: Verify and commit**

Run: `grep -nE ' -o (json|tsv|none|table|yaml)| -otsv| -ojson' Makefile-az.mk`
Expected: no matches.

```bash
git add Makefile-az.mk
git commit --no-verify -m "style: normalize az CLI output flags to long form in Makefile-az.mk"
```

---

### Task 6: `README.md`

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add --output json to captured mutating commands**

1. `KMSI_JSON=$(az identity create ...)` — add `--output json`:
```bash
# Before:
KMSI_JSON=$(az identity create --name karpentermsi --resource-group "${RG}" --location "${LOCATION}")
# After:
KMSI_JSON=$(az identity create --name karpentermsi --resource-group "${RG}" --location "${LOCATION}" --output json)
```

2. `AKS_JSON=$(az aks create ...)` — add `--output json` at end of the multi-line command:
```bash
# Before:
  --enable-oidc-issuer --enable-workload-identity)
# After:
  --enable-oidc-issuer --enable-workload-identity \
  --output json)
```

- [ ] **Step 2: Style-normalize existing flags**

```bash
# Before:
RG_MC_RES=$(az group show --name "${RG_MC}" --query "id" -otsv)
# After:
RG_MC_RES=$(az group show --name "${RG_MC}" --query "id" --output tsv)
```

- [ ] **Step 3: Verify and commit**

Run: `grep -n '\baz ' README.md | grep -v '#'`
Confirm: captured `az` commands have `--output json`, `--query` usages have `--output tsv`. No short-form flags.

```bash
git add README.md
git commit --no-verify -m "fix: add explicit --output flags to az commands in README.md"
```

---

### Task 7: `docs/workshops/1_aks_cluster_creation_and_install_karpenter.md`

**Files:**
- Modify: `docs/workshops/1_aks_cluster_creation_and_install_karpenter.md`

- [ ] **Step 1: Add --output json to captured commands**

1. `KMSI_JSON=$(az identity create ...)` (line ~88):
```bash
# Before:
KMSI_JSON=$(az identity create --name karpentermsi --resource-group "${RG}" --location "${LOCATION}")
# After:
KMSI_JSON=$(az identity create --name karpentermsi --resource-group "${RG}" --location "${LOCATION}" --output json)
```

2. `AKS_JSON=$(az aks create ...)` (line ~94):
```bash
# Before:
  --enable-oidc-issuer --enable-workload-identity)
# After:
  --enable-oidc-issuer --enable-workload-identity \
  --output json)
```

3. Recovery commands in the note block (line ~108-109):
```bash
# Before:
> AKS_JSON=$(az aks show --name "${CLUSTER_NAME}" --resource-group "${RG}")
> KMSI_JSON=$(az identity show --name karpentermsi --resource-group "${RG}")
# After:
> AKS_JSON=$(az aks show --name "${CLUSTER_NAME}" --resource-group "${RG}" --output json)
> KMSI_JSON=$(az identity show --name karpentermsi --resource-group "${RG}" --output json)
```

- [ ] **Step 2: Style-normalize existing flags**

```bash
# Before:
RG_MC_RES=$(az group show --name "${RG_MC}" --query "id" -otsv)
# After:
RG_MC_RES=$(az group show --name "${RG_MC}" --query "id" --output tsv)
```

- [ ] **Step 3: Verify and commit**

```bash
git add docs/workshops/1_aks_cluster_creation_and_install_karpenter.md
git commit --no-verify -m "fix: add explicit --output flags to az commands in workshop docs"
```

---

### Task 8: `CONTRIBUTING.md`

**Files:**
- Modify: `CONTRIBUTING.md`

- [ ] **Step 1: Add developer note**

In the "Developer notes" section (after the existing two bullet points), add:

```markdown
- When writing `az` CLI commands in scripts or documentation, always specify an explicit `--output` format (`--output json`, `--output tsv`, `--output table`, `--output none`) on any command whose output is consumed programmatically. Users may have changed their default output format via `az configure`, which breaks scripts that assume JSON output. Use `--output tsv` with `--query` for single-value extraction; use `--output json` when piping to `jq`.
```

- [ ] **Step 2: Commit**

```bash
git add CONTRIBUTING.md
git commit --no-verify -m "docs: add az CLI --output convention to developer notes"
```

---

### Task 9: Final verification

- [ ] **Step 1: Verify no consumed-output commands lack explicit format**

Run from the repo root:
```bash
# Find az commands piped to jq without --output
grep -rn 'az .*| *jq' --include='*.sh' --include='*.mk' --include='*.yaml' --include='*.md' Makefile* hack/ docs/ README.md .github/ | grep -v '\-\-output' | grep -v '^ *#'
```
Expected: no matches (every `az ... | jq` now has `--output json`).

```bash
# Find az commands with --query but no --output
grep -rn 'az .*--query' --include='*.sh' --include='*.mk' --include='*.yaml' --include='*.md' Makefile* hack/ docs/ README.md .github/ | grep -v '\-\-output' | grep -v '^ *#'
```
Expected: no matches.

- [ ] **Step 2: Verify no short-form flags remain**

```bash
grep -rnE ' -o (json|tsv|none|table|yaml)| -otsv| -ojson' --include='*.sh' --include='*.mk' --include='*.yaml' --include='*.md' Makefile* hack/ docs/ README.md .github/
```
Expected: no matches.

- [ ] **Step 3: Review git log**

```bash
git --no-pager log --oneline -10
```

Confirm 7-8 commits covering all files.

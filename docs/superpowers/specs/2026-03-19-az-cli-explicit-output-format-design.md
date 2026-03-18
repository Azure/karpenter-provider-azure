# Azure CLI Explicit Output Format

## Problem

All `az` CLI commands in the repository that consume output programmatically assume the user's default output format is JSON. If a user has changed their default via `az configure` (e.g., to `table` or `yaml`), scripts break silently — `jq` receives non-JSON input, variable captures contain unexpected formats, and `--query` results render differently.

~66% of the ~143 `az` commands across 18 files lack an explicit `--output` flag. Of those, roughly 15-20 have their output consumed programmatically (piped to `jq`, captured into variables parsed with `jq`, or using `--query` without a format).

## Scope

Fix every `az` command whose output is consumed programmatically. Leave commands whose output is only displayed to the user untouched.

### Files in scope

**Shell scripts:**
- `hack/deploy/configure-values.sh`
- `hack/deploy/create-cluster.sh`
- `hack/codegen.sh` (style normalization only — already has explicit formats)

**Makefile:**
- `Makefile-az.mk`

**Documentation:**
- `README.md`
- `docs/workshops/1_aks_cluster_creation_and_install_karpenter.md`

**CI:**
- `.github/actions/` YAML files (only commands that consume output programmatically)

**Contributing guide:**
- `CONTRIBUTING.md` — add developer note about the convention

## Rules

1. Every `az` command whose output is consumed programmatically must have an explicit `--output` flag.
2. Commands whose output is only displayed to the user are left untouched.
3. All format flags use the long form (`--output tsv`, not `-o tsv` or `-otsv`).

## Transformation Patterns

### Pattern 1: Simple `jq` to `--query --output tsv`

When `jq` extracts a single field with a simple path (e.g., `.foo`, `.foo.bar`, `.[0].baz`), replace the `jq` pipe with `--query` and `--output tsv`.

```bash
# Before:
az aks show --name "$CLUSTER" --resource-group "$RG" | jq -r ".agentPoolProfiles[0].vnetSubnetId"

# After:
az aks show --name "$CLUSTER" --resource-group "$RG" --query "agentPoolProfiles[0].vnetSubnetId" --output tsv
```

### Pattern 2: Captured variable + simple `jq` to `--query --output tsv`

When output is captured into a variable and later a single field is extracted with `jq`, collapse into a single `az` call with `--query --output tsv`.

```bash
# Before:
KMSI_JSON=$(az identity create --name karpentermsi --resource-group "${RG}" --location "${LOCATION}")
KARPENTER_USER_ASSIGNED_CLIENT_ID=$(echo "${KMSI_JSON}" | jq -r '.clientId')

# After:
KARPENTER_USER_ASSIGNED_CLIENT_ID=$(az identity create --name karpentermsi --resource-group "${RG}" --location "${LOCATION}" --query "clientId" --output tsv)
```

**Exception:** If the same captured variable is consumed multiple times extracting different fields, AND the command is mutating (`az create`, `az update`), keep the captured variable approach and add `--output json`. Do not re-run a mutating command to extract each field. For read-only commands (`az show`, `az list`), separate `--query --output tsv` calls are acceptable.

### Pattern 3: Complex `jq` — add `--output json`

When `jq` does more than single-field extraction (filters, conditionals, array indexing like `.[0]`, multiple fields), keep the `jq` pipe and add `--output json`.

```bash
# Before:
az network vnet list --resource-group "$resource_group" | jq -r ".[0]"

# After:
az network vnet list --resource-group "$resource_group" --output json | jq -r ".[0]"
```

### Pattern 4: `--query` without `--output`

Any command using `--query` without an explicit `--output` flag gets one added. Use `--output tsv` for scalar value extraction, `--output json` for queries returning arrays/objects.

```bash
# Before:
az resource list --tag=karpenter.sh_nodepool --query "[?resourceGroup=='foo']"

# After:
az resource list --tag=karpenter.sh_nodepool --query "[?resourceGroup=='foo']" --output json
```

### Pattern 5: Style normalization

All existing short-form output flags are expanded to long form for consistency:

- `-o json` → `--output json`
- `-otsv` / `-o tsv` → `--output tsv`
- `-o none` → `--output none`
- `-o table` → `--output table`

This applies to ALL `az` commands in the repo, including those whose output is only displayed.

## CONTRIBUTING.md Addition

Add to the "Developer notes" section:

> When writing `az` CLI commands in scripts or documentation, always specify an explicit `--output` format (`--output json`, `--output tsv`, `--output table`, `--output none`) on any command whose output is consumed programmatically. Users may have changed their default output format via `az configure`, which breaks scripts that assume JSON output. Use `--output tsv` with `--query` for single-value extraction; use `--output json` when piping to `jq`.

## Testing

Verification is manual:
1. `grep -rn '\baz ' --include='*.sh' --include='*.mk' --include='*.yaml' --include='*.md' Makefile*` to review all commands
2. Confirm every command that pipes to `jq`, captures into a variable parsed by `jq`, or uses `--query` has an explicit `--output` flag
3. Confirm all format flags use long form `--output`
4. Run existing CI/scripts against a test cluster to verify no regressions (if available)

## Out of Scope

- Adding `-o none` to commands whose output is not consumed (fire-and-forget commands)
- Adding new lint tooling or CI checks for this convention
- Refactoring complex `jq` pipelines beyond adding `--output json`

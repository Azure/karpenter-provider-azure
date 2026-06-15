# Non-AKS Environments

**Author:** @hakman

**Last updated:** Jul 2, 2026

**Status:** Completed

## Overview

Karpenter Provider Azure currently generates Azure VM `OSProfile.CustomData` through AKS bootstrap paths. This design adds a dedicated `PROVISION_MODE=userdata` mode for standalone Azure Compute VMs where an external bootstrap owner supplies the complete payload.

In `userdata` mode, `AKSNodeClass.spec.userData` is required and is the full raw cloud-init or shell payload. The provider base64-encodes it exactly once for Azure `OSProfile.CustomData`. The payload owns everything from boot to node registration and must satisfy the node registration contract below.

The first version must also include non-AKS marketplace images and explicit NSG references, and bypass the AKS launch assumptions that would otherwise make non-AKS bootstrap unusable: AKS bootstrap rendering, CSE, AKS load balancer/NSG lookups, AKS billing tags and extensions, and extra NIC IP configurations.

### Goals

- Add raw `spec.userData` as a complete bootstrap replacement.
- Define the node registration contract the user data payload must satisfy.
- Map `spec.imageFamily` to Azure marketplace images in userdata mode.
- Add `spec.networkSecurityGroupID` for an explicit NIC-level NSG.
- Add explicit `PROVISION_MODE=userdata` semantics.
- Keep existing AKS bootstrap behavior unchanged.
- Keep NodeClass readiness, static drift, and image drift meaningful.
- Fail fast on cross-mode misconfiguration: user data or explicit NSG references in AKS modes, and AKS-only options in userdata mode.

### Non-Goals

- No merging user data with AKS bootstrap.
- No AKS Machine API user data support.
- No new NodeClass kind in the first version.
- No mixing of AKS-bootstrapped and userdata NodePools in the same cluster; provision mode is cluster-wide.
- No requirement for a `Custom` image family gate.
- No safe secret storage for inline user data.
- No switch to Azure's newer VM `userData` property.
- No explicit custom image/imageID API in the first version.
- No image-owned bootstrap with empty user data in the first version; it only makes sense with pre-baked custom images, so it returns with that follow-up.
- No creation or management of networking resources; users provide existing resource IDs.
- No explicit load balancer backend pool IDs in the first version.

## API

Add `spec.userData`:

```go
// userData is the complete raw bootstrap payload (cloud-init or shell script) for userdata provision mode
// (PROVISION_MODE=userdata). The provider base64-encodes it exactly once for Azure OSProfile.CustomData.
// Required in userdata mode; must not be set in other modes.
// +kubebuilder:validation:MaxLength=65535
// +optional
UserData *string `json:"userData,omitempty"`
```

Semantics:

- non-empty: raw payload; provider base64-encodes it for Azure.
- `""`/omitted/null: invalid in `userdata` mode (CRD-optional only for AKS modes).
- whitespace-only values are invalid.

`userData` is stored in plain text in etcd, write-only in ARM, and readable from inside the VM via IMDS.

In `userdata` mode, `imageFamily` maps to marketplace URNs:

- `Ubuntu` / `Ubuntu2404`:
  - amd64 Gen2: `Canonical:ubuntu-24_04-lts:server`
  - amd64 Gen1: `Canonical:ubuntu-24_04-lts:server-gen1`
  - arm64 Gen2: `Canonical:ubuntu-24_04-lts:server-arm64`
- `Ubuntu2204`:
  - amd64 Gen2: `Canonical:ubuntu-22_04-lts:server`
  - amd64 Gen1: `Canonical:ubuntu-22_04-lts:server-gen1`
  - arm64 Gen2: `Canonical:ubuntu-22_04-lts:server-arm64`
- `AzureLinux` (3):
  - amd64 Gen2: `MicrosoftCBLMariner:azure-linux-3:azure-linux-3-gen2`
  - amd64 Gen1: `MicrosoftCBLMariner:azure-linux-3:azure-linux-3`
  - arm64 Gen2: `MicrosoftCBLMariner:azure-linux-3:azure-linux-3-arm64`
- `AzureLinux` (2):
  - amd64 Gen2: `MicrosoftCBLMariner:cbl-mariner:cbl-mariner-2-gen2`
  - amd64 Gen1: `MicrosoftCBLMariner:cbl-mariner:cbl-mariner-2`
  - arm64 Gen2: `MicrosoftCBLMariner:cbl-mariner:cbl-mariner-2-arm64`

Mappings are version-less and ordered most-preferred first; `AzureLinux` 3 vs 2 follows Kubernetes version as in AKS modes. The reconciler resolves each to the newest concrete version in the operator's region (see Readiness).

Add `spec.networkSecurityGroupID`:

```go
// networkSecurityGroupID is the Azure NSG resource ID attached to the primary NIC in userdata provision mode.
// If omitted, no NIC-level NSG is attached. Only valid in userdata mode.
// +kubebuilder:validation:Pattern=`(?i)^\/subscriptions\/[^\/]+\/resourceGroups\/[a-zA-Z0-9_\-().]{0,89}[a-zA-Z0-9_\-()]\/providers\/Microsoft\.Network\/networkSecurityGroups\/[^\/]+$`
// +optional
NetworkSecurityGroupID *string `json:"networkSecurityGroupID,omitempty"`
```

`networkSecurityGroupID` must be a full `Microsoft.Network/networkSecurityGroups` resource ID. It is attached to the primary NIC in `userdata` mode. The provider must not discover the AKS managed NSG in this mode. Attaching it requires `Microsoft.Network/networkSecurityGroups/join/action` on the NSG for the Karpenter identity.

If both `v1alpha2` and `v1beta1` remain served, add all new fields to both versions and regenerate deepcopy/CRD output.

## Provision Mode Behavior

`PROVISION_MODE=userdata` means:

- Standalone Azure Compute VM path only.
- `spec.userData` is required and is the complete bootstrap payload.
- `spec.imageFamily` resolves to marketplace images.
- Do not call AKS scriptless bootstrap.
- Do not call the node bootstrapping client.
- Do not create CSE.
- Do not create the AKS identifying/billing extension.
- Do not add `compute.aks.billing=linux`.
- Do not discover AKS load balancer backend pools.
- Do not discover the AKS managed NSG.
- Attach `spec.networkSecurityGroupID` to the primary NIC when set; otherwise leave NIC-level NSG unset.
- Create only the primary NIC IP configuration, regardless of `spec.maxPods`.
- Require `spec.maxPods`; ignore `network-plugin`/`network-plugin-mode`.
- Continue applying Karpenter ownership and NodePool tags needed for listing and cleanup.
- Keep the `aks-` resource name prefix; naming is identical across modes.

Existing slash-to-underscore tag normalization must be documented because it can change external ownership tag keys.

Provision mode is cluster-wide and fixed for the lifetime of a node resource group; mixing or switching modes is unsupported, and enforcement is up to the operator. A switch flips NodeClass validation and swaps `Status.Images` between gallery and marketplace, rolling every node by image drift.

## Node Registration Contract

Karpenter only creates the VM. The user data payload must guarantee:

- Kubelet registers with the `karpenter.sh/unregistered:NoExecute` startup taint (e.g. `--register-with-taints`). Karpenter removes it after syncing NodeClaim labels, annotations, and taints onto the Node, so per-node values must not be baked into user data. Without the taint the node still registers, with a warning and no registration race protection.
- `spec.providerID` matches `azure:///subscriptions/<sub>/resourceGroups/<node rg>/providers/Microsoft.Compute/virtualMachines/<vm name>` (resource group lowercased), as produced by cloud-provider-azure or kubelet `--provider-id`. On mismatch the NodeClaim never registers and the VM is deleted after the registration TTL.
- `spec.userData` is static per NodeClass; per-node values come from the environment (IMDS). Hostname is the VM name, `aks-<NodeClaim name>`.
- Kubelet `--max-pods` matches `spec.maxPods`.

## Validation and Readiness

CRD validation:

- `spec.userData` max length: 65,535 characters.
- `spec.networkSecurityGroupID`, when set, is a valid NSG resource ID.
- Both fields remain optional so existing NodeClasses are unchanged.

Runtime/status validation:

- In `userdata` mode, `spec.userData` must be set, non-empty, non-whitespace, and no more than 65,535 bytes before encoding.
- In AKS modes, including AKS Machine API modes, `spec.userData` and `spec.networkSecurityGroupID` are invalid.
- In `userdata` mode, reject `spec.kubelet`, `spec.localDNS`, `spec.linuxOSConfig`, `spec.artifactStreaming`, and `spec.fipsMode: FIPS`; they are AKS bootstrap, AKS Machine API, or unsupported image inputs.
- Keep `spec.gpu` valid in `userdata` mode: `gpu.mode` still controls GPU instance-type filtering (`Driver` keeps only SKUs with managed driver support; `None` allows all GPU SKUs), but no GPU drivers are installed by the provider in this mode regardless — the user data payload or image owns driver installation.
- In `userdata` mode, `spec.maxPods` is required; the payload or image must configure kubelet to match. The network-plugin derived defaults do not apply.

Operator option validation:

- Accept `PROVISION_MODE=userdata`.
- In `userdata` mode, do not require `cluster-endpoint` or `kubelet-bootstrap-token`.
- Continue requiring `cluster-name`, `ssh-public-key`, `vnet-subnet-id`, and `node-resource-group`.
- In `userdata` mode, reject `use-sig=true`, `nodebootstrapping-server-url`, and `aks-machines-pool-name`: they select other provisioning or image paths.
- Other AKS bootstrap options (`cluster-endpoint`, `kubelet-bootstrap-token`, `dns-service-ip`, `kubelet-identity-client-id`, `network-plugin`, `network-plugin-mode`) are unused when set, though still format-validated.

Readiness:

- Keep `KubernetesVersionReady` as a hard readiness condition.
- Keep `ImagesReady` as a hard readiness condition.
- The image reconciler resolves `spec.imageFamily` to the newest concrete marketplace version per publisher/offer/SKU (`VirtualMachineImagesClient`, operator's region, shared TTL cache); SKUs unavailable in the region are skipped.
- Marketplace mappings carry architecture and Hyper-V generation requirements so incompatible instance types are filtered.
- `Status.Images` stores marketplace images as `/Publishers/{p}/ArtifactTypes/VMImage/Offers/{o}/Skus/{s}/Versions/{v}`; the `/Versions/{v}` suffix keeps existing base-ID version pinning working. Versions are always concrete, never `latest`.
- Version bumps follow the existing gates: full refresh when `ImagesReady` is false or a maintenance window is open; otherwise versions stay pinned.

## Launch Template and VM Flow

Reuse the existing launch template custom data fields:

```go
type Template struct {
    ScriptlessCustomData    string
    CustomScriptsCustomData string
    CustomScriptsCSE        string

    // ImageID is the stable desired image identifier used for status, metrics, and drift.
    ImageID string

    SubnetID string
    // existing storage/tag fields...
}
```

Mode-specific custom data:

- `aksscriptless`: set `ScriptlessCustomData` from `params.ScriptlessCustomData.Script()`.
- `bootstrappingclient`: set `CustomScriptsCustomData` and `CustomScriptsCSE` from `GetCustomDataAndCSE(ctx)`.
- `userdata`: base64-encode `spec.userData` into `ScriptlessCustomData`.

`newVMObject` assigns these fields directly to `OSProfile.CustomData`; it must not encode them again.
`BeginCreate` creates CSE only in `bootstrappingclient` mode.

For `userdata`, keep the shared launch parameter path but skip AKS managed NSG/route-table discovery. VM creation still needs image reference, subnet ID, storage profile, OS marker, tags, and explicit NSG ID. `setImageReference` parses the marketplace image ID into a `Publisher`/`Offer`/`SKU`/`Version` reference instead of a gallery ID.

## Drift

Both new fields are hashed automatically (no `hash:"ignore"`).

Keep `AKSNodeClassHashVersion` at `v3`: the fields have no defaults and are absent on existing NodeClasses, so existing hashes are unchanged. A bump would backfill NodeClaim hash annotations on upgrade and mask pending drift.

Hash tests must cover:

- adding the fields leaves existing NodeClass hashes unchanged, and non-empty `spec.userData` changes trigger drift;
- `networkSecurityGroupID` changes;
- both served API versions, if both remain served.

Image drift remains active through `AKSNodeClass.Status.Images`; marketplace version bumps drift under the readiness gates above. `NodeClaim.Status.ImageID` renders marketplace images as URNs (`Publisher:Offer:Sku:Version`), so drift comparison parses `Status.Images` IDs to URNs.

Kubelet identity drift is not mode-gated: userdata nodes are unaffected only because they never carry the `kubernetes.azure.com/kubelet-identity-client-id` node label and missing labels do not count as drift. Removing that short-circuit (existing TODO in `drift.go`) would drift every userdata node when `kubelet-identity-client-id` is set.

## Example

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: custom-workers
spec:
  vnetSubnetID: /subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/cluster-rg/providers/Microsoft.Network/virtualNetworks/cluster-vnet/subnets/nodes
  networkSecurityGroupID: /subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/cluster-rg/providers/Microsoft.Network/networkSecurityGroups/workers-nsg
  imageFamily: Ubuntu2404
  maxPods: 110
  tags:
    # '/' in tag keys is replaced with '_' on the Azure resources
    cluster.example.com/name: example
    cluster.example.com/role: worker
  userData: |
    #cloud-config
    # Complete bootstrap payload rendered by an external bootstrap owner.
```

## Testing

- API/CRD: user data size limit and NSG ID validation.
- Options: `userdata` accepted; required options relaxed; path-selecting options rejected; bootstrap options tolerated.
- NodeClass validation: missing or whitespace user data in `userdata` mode, unsupported AKS fields, unsupported AKS modes, FIPS rejection, and AKS Machine API rejection.
- Image readiness: marketplace mappings resolve to the newest concrete version, skip unavailable SKUs, and stay pinned outside maintenance windows.
- Marketplace image identity: ID build/parse round-trip, URN rendering for `NodeClaim.Status.ImageID`, and drift comparison matching the two formats.
- Launch template: raw user data decodes correctly at the VM boundary; no AKS bootstrap content appears.
- VM provider: no CSE, AKS identifying extension, AKS billing tag, AKS LB/NSG lookups, or extra NIC IP configurations in user data mode; explicit NSG ID attaches to the NIC.
- Max pods: required in `userdata` mode.
- Tags and names: Karpenter/NodePool tags exist, slash normalization covered, naming identical across modes.
- Drift: user data and network security group hash changes, hash version unchanged at `v3`, and image drift for resolved marketplace version changes.
- E2E: manual validation against a kOps-managed Azure cluster.

## Open Follow-Ups

- Add an explicit `spec.imageID` API. It covers custom images, pins a concrete marketplace version when needed, and enables image-owned bootstrap (empty user data omitting `OSProfile.CustomData`).
- Add optional load balancer backend pool IDs or route table handling.
- Decide whether non-AKS mode needs a separate public NodeClass kind or a per-NodeClass provision mode; that is also the path to mixing AKS and userdata NodePools in one cluster.

## References

- [AKSNodeClass spec and hash](../pkg/apis/v1beta1/aksnodeclass.go)
- [Launch template custom data flow](../pkg/providers/launchtemplate/launchtemplate.go)
- [VM custom data assignment and extension flow](../pkg/providers/instance/vminstance.go)
- [Operator option validation](../pkg/operator/options/options_validation.go)
- [Marketplace image mapping and identity](../pkg/providers/imagefamily/marketplace.go)
- [Marketplace image version resolution](../pkg/providers/imagefamily/nodeimage.go)
- [Azure custom data and cloud-init on virtual machines](https://learn.microsoft.com/en-us/azure/virtual-machines/custom-data)
- [AWS EC2NodeClass userData API](https://github.com/aws/karpenter-provider-aws/blob/main/pkg/apis/v1/ec2nodeclass.go)
- [AWS custom AMI user data behavior](https://github.com/aws/karpenter-provider-aws/blob/main/pkg/providers/amifamily/custom.go)
- [AKS node bootstrap design](./0001-aks-node-bootstrap.md)

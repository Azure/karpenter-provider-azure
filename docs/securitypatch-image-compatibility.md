# SecurityPatch images for new managed NAP nodes

When configured by AKS with `NODE_OS_UPGRADE_CHANNEL=SecurityPatch`, SIG image
discovery and a Machine API provisioning mode, the controller prefers compatible
captured SecurityPatch images for **new nodes**. This is a best-effort preference,
not a guarantee that every new node uses a security-patched image.

Scheduling and standard image discovery remain unchanged. After choosing a VM type
and building its standard Machine template, Karpenter looks for a captured image
with the same OS and image definition. It keeps the standard NIV if none is available.
Fallback preserves the requested image family, FIPS, Trusted Launch, Kata runtime,
architecture and generation. It does not change those requirements to manufacture
a match. If neither catalog has a compatible image, capacity remains unavailable.

The standard and captured catalogs are queried separately. Each new Machine create
performs an optional captured lookup, bounded to five seconds including queue time
and paging, with at most four concurrent lookups per provider. Results are not cached
across requests. SecurityPatch discovery
requests opt into `CapturedImagesOnly: true`; existing callers requesting the full
SecurityPatch catalog can still discover abstract live-patching targets. A captured
image may also represent a patch that does not require a reboot: these properties
are independent.

## Existing nodes

Enabling the preference does not initiate migration of existing standard-image
nodes to SecurityPatch. Their existing standard-image maintenance behavior remains.
Captured-image nodes are not image-drifted merely because a newer captured image
appears, captured coverage disappears, or the channel is disabled. Other replacement
reasons, including Kubernetes-version and NodeClass configuration changes, remain.
Any resulting new node uses the current provisioning preference.

This implementation does not live-patch existing nodes. In-place patching is future
work and would not by itself provide images for initial scale-out.

## Operator-visible fallback

- `status.images` remains the standard-image baseline. No new NodeClass API field
  or background coverage condition is introduced.
- New NodeClaims record `karpenter.azure.com/image-selection` as `SecurityPatch`
  or `StandardImageFallback`. `karpenter.azure.com/image-selection-reason` distinguishes
  `NoCompatibleCapturedImage`, `CatalogUnavailable`, `ImageUnavailable` and recovery
  of an `ExistingMachine`. Successful fallback create completions emit a
  `SecurityPatchFallback` event. Structured logs record the selected NIV and reason.
- If the service rejects a captured version with `SecurityVHDNotFound`, the claim
  records `karpenter.azure.com/securitypatch-fallback=ImageUnavailable` and retries
  with an explicitly selected standard image. Other create errors are not silently
  converted into fallback.

Inspect both image lists and the requirements of the NodePool and pending pod:

```sh
kubectl get aksnodeclass <name> -o yaml
kubectl get nodepool <name> -o yaml
kubectl describe pod <name> -n <namespace>
kubectl describe nodeclaim <name>
```

The existing standard-image cache is unchanged. Captured discovery failure or timeout
uses the already-selected standard image; parent cancellation aborts the attempt.
An existing Machine is reused before looking up another image. On the active
SecurityPatch channel, a failed post-create GET or incomplete non-failed response
does not trigger deletion in that attempt. Other channels retain their existing
cleanup behavior, even if a claim carries an old SecurityPatch fallback annotation.
Acceptance is not persisted across attempts: if a subsequent lookup returns 404,
the existing create flow can select an image and submit another PUT. API errors, regional image
replication, SKU availability and quota can still prevent provisioning; fallback
does not hide unrelated failures.

## Compatibility boundaries

Evaluate the deployed controller/CRDs and regional catalog, not only the cluster
channel. These are configurations that may require standard-image fallback when
the corresponding captured definitions are not published:

| Configuration | Required captured definitions |
| --- | --- |
| Azure Linux Kata | `V3katagen2` |
| Ubuntu 20.04 FIPS | `2004gen2fipscontainerd`, `2004fipscontainerd` |
| Azure Linux 2 | `V2gen2`, `V2`, `V2gen2arm64`, and applicable FIPS/TL variants |
| Azure Linux 3 Gen1 | `V3`, `V3fips` |
| Ubuntu 22.04 Gen1 FIPS | `2204fipscontainerd` |

Generic AzureLinux resolves to version 3 at Kubernetes 1.32+. Generic Ubuntu FIPS
resolves to Ubuntu 22.04 at Kubernetes 1.35+ or with Trusted Launch, otherwise to
Ubuntu 20.04. Earlier controller versions used different cutovers.

Do not confuse catalog gaps with unsupported feature combinations. The current
CRDs reject explicit Ubuntu2404+FIPS, AzureLinux+FIPS+Trusted Launch, and Kata with
Ubuntu, FIPS, or Trusted Launch. Standard fallback does not make those valid.
Ubuntu FIPS and Trusted Launch candidates have architecture restrictions; Kata
requires an eligible amd64 nested-virtualization-capable VM. A Gen2 candidate alone
does not cover a workload restricted to Gen1-only sizes.

Image owners must build and replicate additional captured variants to eliminate
fallback. This document is not a commitment that every listed variant is available
in every region.

## Deployment

Deploy service support for captured discovery and exact SecurityPatch Machine
resolution before enabling the controller preference. The service must continue
accepting explicitly selected standard NIVs on SecurityPatch NAP clusters.
No status-schema rollout is required. The controller and service must report the
actual selected version, never substitute an image invisibly.

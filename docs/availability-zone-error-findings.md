# Availability-zone rejection findings

## Summary

A rejected SKU-and-zone placement does not establish that the zone is unavailable
for every SKU. In particular, AKS Machines `AvailabilityZoneNotSupported` can
reject a placement that works with another SKU.

The provider should exclude only placements supported by the evidence, retry
after four hours, and leave ordinary capacity and quota handling unchanged.

## Live observations

The following actual Machines PUT/GET probes ran in East US 2 on 2026-10-27,
using API version `2026-07-02-preview` and Kubernetes 1.36. The two missing-zone
SKUs had no subscription restrictions in the subscription SKU catalog.

| SKU | Zone | Subscription catalog | Result |
| --- | --- | --- | --- |
| `Standard_D2ads_v7` | 1 | Zone absent | Synchronous `AvailabilityZoneNotSupported` |
| `Standard_D2ads_v7` | 2 | Zone advertised | Provisioning succeeded |
| `Standard_E2s_v5` | 1 | Zone advertised | Provisioning succeeded |
| `Standard_DC2s_v3` | 2 | Zone absent | Synchronous `AvailabilityZoneNotSupported` |
| `Standard_DC2s_v3` | 1 | Zone advertised | Synchronous `AvailabilityZoneNotSupported` |
| `Standard_D2ds_v5` | 3 | Zone advertised but subscription-restricted | PUT accepted; asynchronous `SkuNotAvailable` |
| `Standard_D2ds_v5` | 4 | Zone advertised | Synchronous `AvailabilityZoneNotSupported` |

The error's "supported zones for location" list differed by SKU: `2,3` for
`D2ads_v7`, `3` for `DC2s_v3`, and `3,1,2` for `D2ds_v5`. The message therefore
must not be interpreted as a region-wide supported-zone list.

Earlier Compute probes rejected `D2ds_v5` and `E2s_v5` in zone 4 with
`InvalidAvailabilityZone`, while regional `D2ds_v5` provisioning succeeded.
These observations establish those rejected placements, not a universal contract
that this code invalidates a zone for all SKUs and configurations.

All disposable clusters, Machines, resource groups, and SSH keys used for these
probes were removed and their absence verified.

## Error paths and limitations

### Machine frontend validation

The live comparisons establish that this rejection is SKU-specific and is not
simply a current-capacity signal. A catalog-advertised placement can still be
rejected, so catalog filtering alone is insufficient.

The safe scope for the frontend SKU-zone membership rejection is the exact SKU
and zone. Priority and capacity reservations do not make a SKU support another
zone; other SKUs and sizes must remain eligible.

### Compute provisioning

Treat `InvalidAvailabilityZone` as rejection of the attempted offering. Without
a broader service contract, do not propagate it to other sizes, capacity types,
or capacity reservation groups.

### Machine asynchronous failures

Machine GET errors can have nested details. A source-derived explanation predicts
that an underlying Compute `InvalidAvailabilityZone` can be converted into a
generic VM-create failure with its specific code omitted from public details.
That exact wire shape has not yet been captured by these probes.

The SDK can preserve received details but cannot reconstruct an omitted code.
Do not classify a generic VM-create failure as zone invalidity or infer it by
parsing raw messages. Preserve explicit unhandled-error reporting when no
recognized code is available.

### Header-batch creation

The provider's [shared batch template](../pkg/providers/azclient/aksmachinesheaderbatch/batchkey.go)
normally omits zones and supplies them per Machine in the header. UltraSSD
retains zones in the shared template and groups by them. Direct-create validation
results therefore do not establish equivalent behavior for header batches.

Raw GET errors for header-batch invalid-zone requests, and a direct/batch
comparison for the catalog disagreement, still need live verification.

## Provider policy

- Machine frontend `AvailabilityZoneNotSupported`: exact SKU and zone, shared
  across priority types and reservation groups, without family-size widening.
- Compute `InvalidAvailabilityZone`: exact attempted SKU, zone, capacity type,
  and reservation scope, without family-size widening.
- Four-hour TTL for these exclusions; in-memory state clears on restart.
- The cache belongs to the configured provider subscription, region and
  provisioning mode; mode changes do not preserve these in-memory exclusions.
- Other zones and regional placement stay eligible.
- Retain sanitized numeric supported-zone hints, described as applying to the
  attempted SKU rather than every SKU in the region.
- Keep ordinary allocation, quota, and reservation isolation behavior unchanged.
- Test healthy SKUs and larger same-family sizes in the rejected zone, both
  capacity types, reservation scoping, cache generation/expiry, and scheduling
  placement requirements.

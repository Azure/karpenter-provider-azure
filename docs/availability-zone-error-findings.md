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

### Direct versus header-batch follow-up

On 2026-10-28, a new disposable East US 2 cluster exercised the same Machines API
version and Kubernetes version. Header probes used one entry in `BatchPutMachine`,
with zones absent from the shared body and UltraSSD disabled.

| SKU | Zone | Request path | Result |
| --- | --- | --- | --- |
| `Standard_DC2s_v3` | 1 | Direct PUT | Synchronous `AvailabilityZoneNotSupported` |
| `Standard_DC2s_v3` | 1 | Header batch | Provisioning succeeded |
| `Standard_D2ads_v7` | 1 | Header batch | PUT accepted; asynchronous `OverconstrainedZonalAllocationRequest` |
| `Standard_D2ds_v5` | 4 | Header batch | Provisioning succeeded |

The DC comparison proves that the direct-create metadata rejection must not be
assumed to apply to header-batch creation. The zone-4 Machine reported
`provisioningState: Succeeded` and `vmState: Running`; its underlying Compute VM
was also observed with zone `4`. A historically rejected placement therefore
cannot be assumed to remain deployment-invalid.

No asynchronous `InvalidAvailabilityZone` was obtained in this comparison.
These requests did not exercise mixed-zone multi-Machine partial failures or
the UltraSSD shared-zone path.

### Synthetic unsupported-zone wire probe

A separate one-entry header-batch request on 2026-10-28 deliberately requested
`Standard_D2ds_v5` in synthetic zone `9`. This tested the asynchronous error
response, not the original zone-4 incident.

The PUT was accepted, then Machine GET reported `provisioningState: Failed`.
Its `provisioningError` contained only `code` and `message`:

- Code: `FailedToCreateOrUpdateVirtualMachine`.
- Message: the requested zone `9` was unsupported; the supported zones for
  East US 2 were `1,2,3,4`.
- No `details`, subcode, or `InvalidAvailabilityZone` identifier was present.

This confirms that a Machine unsupported-zone failure can reach the provider
without a specific machine-readable zone error code. The underlying Compute
response was not captured, so the precise internal code/subcode conversion
remains unverified.

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

Machine GET errors can have nested details, but the synthetic zone-9 probe
returned only a generic VM-create code and an unsupported-zone message, without
details or a specific zone code. This verifies the public-response limitation,
not the precise internal conversion of an underlying Compute error. The
historical zone-4 case succeeded through header-batch creation during the
follow-up.

The SDK can preserve received details but cannot reconstruct an omitted code.
Do not classify a generic VM-create failure as zone invalidity or infer it by
parsing raw messages. Preserve explicit unhandled-error reporting when no
recognized code is available. Such failures do not trigger this zone exclusion;
reliable classification requires the service to preserve a specific error code.

### Header-batch creation

The provider's [shared batch template](../pkg/providers/azclient/aksmachinesheaderbatch/batchkey.go)
normally omits zones and supplies them per Machine in the header. UltraSSD
retains zones in the shared template and groups by them. Direct-create validation
results therefore do not establish equivalent behavior for header batches.

The live direct/batch comparison above confirms that those paths can differ.
The synthetic probe also confirms that a public asynchronous failure can lack
a specific zone code; do not broaden classification to a generic error to
compensate.

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

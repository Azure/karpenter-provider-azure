---
name: analyze-sku-mix-placement
description: 'Analyze raw Karpenter integration or E2E logs for SKU Mix Placement and capacity recommendation behavior. Use when asked to inspect test.log, capacityrecommendation/capacity_recommendation.go logs, placement API response ordering, filtered VM SKUs or zones, or stages/sku_mix_rank.go local-versus-recommended rankings and produce a summarized per-call Markdown report.'
argument-hint: '<raw-log-path> [report-path]'
---

# Analyze SKU Mix Placement Logs

Generate a Markdown report from raw Karpenter test output with the bundled
[analyzer](./scripts/analyze.py):

```bash
python3 .github/skills/analyze-sku-mix-placement/scripts/analyze.py <raw-log-path> --output <report-path>
```

Use an output path supplied by the user; otherwise write `sku-mix-placement-report.md` beside the
input log. Omit `--output` only when the user asks for the report inline or on stdout.

## Current Log Schema

Recognize records by the exact `message` value; timestamps, stream prefixes, log levels, and caller
line numbers may vary:

- `received SKU Mix Placement API response` from
  `capacityrecommendation/capacity_recommendation.go`, with the raw body in `response`.
- `sorted non-first SKU Mix Placement choice first` from
  `capacityrecommendation/capacity_recommendation.go`, with the locally preferred choice in
  `ourChoice` and the API's first choice in `placementChoice`.
- `compared SKU Mix Placement recommendations with local ranking` from
  `stages/sku_mix_rank.go`, with `capacityType`, `placementScope`, `splitID`, `localRanking`, and
  `recommendedRanking`.

## Interpretation

Treat one NodeClaim recommendation pass as one report call. Group records by both `reconcileID` and
NodeClaim name, then place all observed `(capacityType, placementScope)` combinations under that call.
Within a call, associate a ranking comparison with its raw API response by matching `splitID` to a
placement choice ID. Do not group unrelated reconciles merely because their timestamps are close.

Begin the report with a summary of all differences, followed by the individual call report. In the
summary:

- State total call, response, comparison, and ordering-failure counts.
- Deduplicate ordering failures by capacity type, placement scope, API-first VM size, and locally
  preferred VM size. Report the occurrence count and one example call for each unique ordering issue;
  keep the full API response under that individual call rather than duplicating it in the summary.
- Deduplicate ranking differences within each `(capacityType, placementScope)` by issue type, VM
  family, and affected zones. Derive the family by removing the size component while preserving the
  remaining suffix and version, so `Standard_D2as_v6` and `Standard_D8as_v6` are both
  `Standard_Das_v6`. Report each unique family issue once with its distinct call count, one example
  VM size, and one example call.
- Include the first placement choice UUID from the associated raw response in each summary example,
  alongside the call number and NodeClaim details, when a raw response is available.
- Treat complete SKU removal, partial zone removal, unexpected addition, and genuine reordering as
  distinct issue types. Do not report rank compaction as a separate issue.

For each individual call:

1. Include the NodeClaim name, `reconcileID`, and the UUID of the first placement choice in the
  first emitted raw response. If no raw response was emitted for a cached result, mark the placement
  choice UUID unavailable rather than inferring it.
2. Report whether every raw response had the expected priority order.
   - The absence of `sorted non-first SKU Mix Placement choice first` means the observed response order
     was correct.
   - If a comparison came from the provider cache and therefore has no raw response log, say that no
     ordering error was observed but the raw order was not emitted again.
   - When that error exists, state the API's first choice and the locally expected top choice. Include
     the full, pretty-printed body from the associated `received SKU Mix Placement API response` record.
   - Read the locally expected choice from `ourChoice` and the API's first choice from
     `placementChoice`.
3. Report local-versus-recommended results separately for every observed capacity type and scope.
   - Compare `localRanking` and `recommendedRanking` directly; do not rely only on `differences`.
  - Immediately after every differing comparison, include a compact JSON block copied from the
    comparison record with `capacityType`, `placementScope`, `localRanking`, and
    `recommendedRanking`. This should show the requested ranking and the recommendation returned by
    the placement provider. Omit this block for complete matches.
   - Identify VM sizes removed entirely and zones removed from a retained VM size.
   - Remove entirely filtered VM sizes from the local sequence and compare the remaining sequence with
     the recommended sequence. Rank movement caused only by higher-ranked removals is expected
     compaction, not a true reordering.
   - Call out genuine reordering, unexpected additions, duplicate or missing scope records, and records
     that cannot be associated confidently.
4. Use `✅` for a complete match and `❌` for an ordering or filtering difference. Keep the summary short,
   with diagnostics and raw JSON below it only when needed.

## Required Shape

````markdown
# SKU Mix Placement Analysis

## Summary

### Placement API response order vs expected priority order

#### Ordering issue 1: on-demand, zonal

❌ The API placed `Standard_D2als_v7` ahead of locally preferred `Standard_D2als_v6` in 5 responses.
Example: Call 3 (..., NodeClaim `example-nodeclaim`, first placement choice UUID
`385b083d-4929-4ba7-88dc-f3db6378e142`).

### Placement API compared with local ranking

#### on-demand, zonal

- `Standard_D_v3` family filtered entirely in zones 2, 3; observed in 18 calls. Example size
  `Standard_D2_v3`. Example: Call 2 (..., NodeClaim `example-nodeclaim`, first placement choice UUID
  `04a7d3b6-6229-44b1-a849-1dfdeda75e37`).

## Individual Calls

### Call 1: <timestamp>

NodeClaim: `example-nodeclaim`; reconcileID: `example-reconcile-id`; first placement choice UUID:
`example-placement-choice-id`

**Placement API response order vs expected priority order:** ✅ Correct

**Placement API compared with local ranking (on-demand, zonal):** ❌ Filtered out ...;
remaining rank changes are expected compaction after those removals.

Requested local ranking and received recommendation:

```json
{
  "capacityType": "on-demand",
  "placementScope": "zonal",
  "localRanking": [
    {"vmSize": "Standard_D2as_v6", "zones": ["1", "2", "3"]}
  ],
  "recommendedRanking": []
}
```

**Placement API compared with local ranking (on-demand, regional):** ✅ Match; nothing filtered.
````

The analyzer writes parse and association diagnostics to stderr. Review them before presenting the
report. If any warning indicates an ambiguous or unattached relevant record, disclose that limitation
instead of silently guessing.

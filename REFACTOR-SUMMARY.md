# Instancetype Test Refactor Summary

**Branch:** `refactor/instancetype-test-separation`
**Base:** `pr-1456` (test-reunification branch)
**Date:** 2026-03-06

## Objective

Separate VM-specific end-to-end tests from pure unit tests in `pkg/providers/instancetype/suite_test.go` to improve developer experience and architectural clarity.

## Changes Made

### 1. Created `pkg/cloudprovider/suite_vm_bootstrap_test.go` (520 lines)

**NEW FILE** containing VM-specific E2E tests that exercise the full CloudProvider orchestration flow and inspect VM implementation details:

- **BootstrappingClient Mode Tests** (2 tests)
  - CSE provisioning validation
  - VM re-creation guard behavior

- **AKSScriptless Mode Tests:**
  - **Subnet & CNI Configuration** (5 tests)
    - VNET subnet ID usage
    - Azure CNI label injection
    - Stateless CNI labels for k8s 1.34+
    - Kubenet mode (no cilium labels)

  - **VM Creation Failures** (1 test)
    - NIC cleanup on failure

  - **Custom DNS** (1 test)
    - Custom DNS server configuration

  - **Community Image Gallery** (6 table entries)
    - CIG image selection for Gen1/Gen2/ARM with Ubuntu/AzureLinux

  - **VM Profile** (2 tests)
    - Auto-delete settings for OS disk and NIC
    - Secondary IP configuration

  - **Bootstrap Script** (4 tests)
    - Kubelet version-specific flags
    - Credential provider configuration
    - Karpenter taint injection

  - **Azure CNI Labels** (5 table entries)
    - Network plugin mode validation
    - NETWORK_PLUGIN environment variable

  - **LoadBalancer** (1 test)
    - Backend pool assignment

**Total: ~25 test cases**

### 2. Cleaned Up `pkg/providers/instancetype/suite_test.go`

**BEFORE:** 1,731 lines (mixed E2E and unit tests)
**AFTER:** 1,156 lines (pure unit and SKU filtering tests)
**REMOVED:** 575 lines (-33%)

**What Remains (Pure Unit Tests):**
- **LocalDNS Filtering** (~390 lines)
  - SKU filtering based on LocalDNS mode
  - Kubernetes version compatibility

- **Ephemeral Disk Placement Algorithm** (~200 lines)
  - NVMe vs Cache vs Temp disk selection logic
  - `FindMaxEphemeralSizeGBAndPlacement()` tests

- **SKU Filtering Tests** (~94 lines)
  - Restricted SKU filtering
  - Confidential SKU exclusion
  - GPU SKU filtering by OS
  - Encryption-at-host filtering

- **MaxPods Calculation** (~186 lines)
  - MaxPods formula for different CNI modes
  - NodeSubnet vs Overlay vs kubenet

- **KubeReservedResources** (~130 lines)
  - Resource reservation calculations

- **Instance Type Properties** (~150 lines)
  - Requirements validation
  - Compute capacity checks

**What Was Moved:**
- All VM bootstrap/customData inspection tests → `cloudprovider/suite_vm_bootstrap_test.go`
- All CIG image selection E2E tests → `cloudprovider/suite_vm_bootstrap_test.go`
- All VM profile configuration tests → `cloudprovider/suite_vm_bootstrap_test.go`

## Architectural Rationale

### Why Move to CloudProvider?

The moved tests **exercise what starts in CloudProvider**:

```
CloudProvider.Create(nodeClaim)
  → InstanceTypesProvider.List() (pick SKU)
  → VMInstanceProvider.Create()
    → Build customData with bootstrap script
    → Create NIC with subnet
    → Create VM with configuration
  → Inspect VM.Properties.OSProfile.CustomData
```

These are **integration tests** that validate the full orchestration flow, not just provider logic in isolation.

### Why Keep in InstanceType?

The remaining tests are **pure unit tests** or **provider-layer tests**:

```
instancetype.FindMaxEphemeralSizeGBAndPlacement(sku)
  → Returns (sizeGB, placement)
  → NO provisioning, just algorithm logic

InstanceTypesProvider.List(nodeClass)
  → Filters SKUs based on criteria
  → NO VM creation, just filtering logic
```

These test **provider responsibilities** without requiring full CloudProvider orchestration.

## Benefits

### 1. Clearer Architectural Boundaries
- **cloudprovider tests** = Integration tests (full flow)
- **instancetype tests** = Unit tests (algorithms and filtering)

### 2. Improved Developer Experience
- Developers working on bootstrap scripts know to look in `cloudprovider/suite_vm_bootstrap_test.go`
- Developers working on SKU filtering know to look in `instancetype/suite_test.go`
- No more 1,700-line files to navigate

### 3. Consistency with PR #1456
- Follows the same pattern as `suite_features_test.go`, `suite_offerings_test.go`, `suite_drift_test.go`
- All CloudProvider integration tests now live in `pkg/cloudprovider/`

### 4. Better Test Organization
```
pkg/cloudprovider/
├── suite_features_test.go           (shared: GPU, ephemeral, kubelet config)
├── suite_offerings_test.go          (shared: quota errors, zone failures)
├── suite_drift_test.go              (shared: drift detection)
├── suite_vm_bootstrap_test.go       (NEW: VM-specific customData/bootstrap)
└── suite_integration_test.go        (mode-specific behaviors)

pkg/providers/instancetype/
└── suite_test.go                    (pure unit: algorithms, filtering)
```

## Testing

Run tests to verify:
```bash
# CloudProvider tests (including new VM bootstrap tests)
go test -count=1 ./pkg/cloudprovider/... -run TestCloudProvider

# InstanceType tests (pure unit tests only)
go test -count=1 ./pkg/providers/instancetype/... -run TestAzure
```

## Next Steps

As requested, the next refactor will:
1. Convert pure unit tests from Ginkgo → table-driven Go tests (`testing` package)
2. Keep E2E tests in Ginkgo (better for integration test workflows)
3. Target: `pkg/providers/instancetype/suite_test.go` pure unit sections

Example transformation:
```go
// BEFORE (Ginkgo):
It("should prefer NVMe disk if supported", func() {
    sku := &skewer.SKU{...}
    sizeGB, placement := instancetype.FindMaxEphemeralSizeGBAndPlacement(sku)
    Expect(placement).To(Equal(instancetype.EphemeralDiskPlacementNVMe))
})

// AFTER (Table-driven):
func TestFindMaxEphemeralSizeGBAndPlacement(t *testing.T) {
    tests := []struct{
        name      string
        sku       *skewer.SKU
        wantSize  int32
        wantPlace instancetype.EphemeralDiskPlacement
    }{
        {
            name: "should prefer NVMe disk if supported",
            sku:  &skewer.SKU{...},
            wantSize: 256,
            wantPlace: instancetype.EphemeralDiskPlacementNVMe,
        },
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            gotSize, gotPlace := instancetype.FindMaxEphemeralSizeGBAndPlacement(tt.sku)
            if gotSize != tt.wantSize || gotPlace != tt.wantPlace {
                t.Errorf("got (%d, %v), want (%d, %v)", gotSize, gotPlace, tt.wantSize, tt.wantPlace)
            }
        })
    }
}
```

## Files Changed

| File | Change | Lines |
|------|--------|-------|
| `pkg/cloudprovider/suite_vm_bootstrap_test.go` | **NEW** | +520 |
| `pkg/providers/instancetype/suite_test.go` | Modified | -575 (1,731 → 1,156) |

**Net change:** -55 lines (with better organization)

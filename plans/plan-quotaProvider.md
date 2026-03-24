# Plan: New Quota Provider and Controller

## TL;DR
Add a new quota provider (`pkg/providers/quota/`) that queries Azure Compute Usage API via `armcompute.UsageClient.NewListPager` and a periodic singleton controller (`pkg/controllers/quota/`) that refreshes quota data every 10 minutes. Follow existing provider/controller/fake patterns.

## Data Model
From `quota.json`, each usage entry has:
- `name.value` — internal name (e.g., `"standardBSFamily"`, `"cores"`)
- `name.localizedValue` — display name
- `currentValue` — current usage (int32)
- `limit` — quota limit (int64)

Azure SDK types: `armcompute.Usage`, `armcompute.UsageClientListResponse`, `armcompute.UsageClient.NewListPager(location, options)`

---

## Phase 1: Azure SDK Client Interface and Fake

**Goal:** Define the `UsageAPI` interface in azclient and create a fake implementation for testing.

### Steps

1. **Add `UsageAPI` interface** in `pkg/providers/azclient/azclient.go`
   - Define interface with single method: `NewListPager(location string, options *armcompute.UsageClientListOptions) *runtime.Pager[armcompute.UsageClientListResponse]`
   - Follow pattern of existing interfaces like `SubnetsAPI`, `DiskEncryptionSetsAPI`

2. **Add `usageClient` field to `AZClient` struct** in `pkg/providers/azclient/azclient.go`
   - Add `usageClient UsageAPI` field
   - Add accessor method `UsageClient() UsageAPI`
   - Add parameter to `NewAZClientFromAPI()`

3. **Create real client** in `NewAZClient()` in `pkg/providers/azclient/azclient.go`
   - Use `armcompute.NewUsageClient(cfg.SubscriptionID, cred, opts)` (already imported `armcompute/v7`)

4. **Create fake implementation** in `pkg/fake/usageapi.go`
   - Struct `UsageAPI` with `AtomicPtrSlice[armcompute.Usage]` for test data
   - Implement `NewListPager` returning single-page `runtime.Pager`
   - Support failure on the initial fetch used to start pagination (first `NextPage`), but do not add mid-pagination failure simulation
   - Follow `CommunityGalleryImageVersionsAPI` pattern (simple list pager)
   - Compile-time interface check: `var _ azclient.UsageAPI = &UsageAPI{}`
   - Add `Reset()` to clear fake data and any configured startup error

5. **Wire fake into test environment** in `pkg/test/environment.go`
   - Add `UsageAPI *fake.UsageAPI` to `Environment` struct
   - Initialize in `NewRegionalEnvironment()`
   - Pass to `NewAZClientFromAPI()`

### Files to modify
- `pkg/providers/azclient/azclient.go` — add interface, field, accessor, update constructors
- `pkg/fake/usageapi.go` — new file: fake UsageAPI
- `pkg/test/environment.go` — wire fake into test env

### Tests
- Verify fake implements interface via compile-time check (`var _ azclient.UsageAPI = &UsageAPI{}`)
- Existing tests still pass after wiring fake into `NewAZClientFromAPI()`

### Verification
- Code compiles with `go build ./...`
- Existing tests still pass: `go test ./...`

---

## Phase 2: Quota Provider

**Goal:** Create the quota provider that fetches and caches Azure compute usage/quota data.

### Steps

1. **Create `pkg/providers/quota/quota.go`** with:
   - `Provider` interface:
     ```
     type Provider interface {
       Update(ctx context.Context) error
       GetUsage(familyName string) (bool, armcompute.Usage)
       GetTotalRegionalUsage() (bool, armcompute.Usage)
     }
     ```
   - `DefaultProvider` struct with:
     - `usageClient azclient.UsageAPI` — the Azure SDK client
     - `location string` — region to query
     - `mu sync.RWMutex` — thread-safe access
     - `usages map[string]armcompute.Usage` — cached usage data keyed by `name.value`
     - `cm *pretty.ChangeMonitor` — change detection for logging
   - Constructor: `NewProvider(usageClient azclient.UsageAPI, location string) *DefaultProvider`
   - `Update(ctx)` method: calls `usageClient.NewListPager(location, nil)`, iterates all pages into a fresh local map, and only swaps the cached map under write lock after the full refresh succeeds
   - On refresh error, retain the previous cached data and log the error
   - `GetUsage(familyName)` method: read-lock, lookup in map, return `(bool, armcompute.Usage)`
   - `GetTotalRegionalUsage()` method: read-lock, lookup `"cores"` key, return `(bool, armcompute.Usage)`
   - Add `Reset()` to clear cached quota data for shared test reuse

### Tests
- **Add quota provider tests** in `pkg/providers/quota/suite_test.go`
  - Set up fake `UsageAPI` with test data matching quota.json structure
  - Test `Update` populates the internal map
  - Test `GetUsage` returns `(true, armcompute.Usage)` with correct values
  - Test `GetTotalRegionalUsage` returns `(true, armcompute.Usage)` for the `"cores"` entry
  - Test that empty responses are handled gracefully (returns `false`)
   - Test that a failed refresh preserves the previously cached quota data
- **Wire quota provider into `pkg/test/environment.go`**
  - Add `QuotaProvider *quota.DefaultProvider` to `Environment` struct
  - Initialize with fake `UsageAPI`
   - Update `Environment.Reset()` to reset both `UsageAPI` and `QuotaProvider`

### Files to create/modify
- `pkg/providers/quota/quota.go` — new file: provider implementation
- `pkg/providers/quota/suite_test.go` — new file: provider tests
- `pkg/test/environment.go` — add QuotaProvider

### Verification
- `go build ./pkg/providers/quota/...`
- `go test ./pkg/providers/quota/...` passes

---

## Phase 3: Quota Controller

**Goal:** Create a singleton controller that periodically updates the quota provider.

### Steps

1. **Create `pkg/controllers/quota/controller.go`** following the instancetype controller pattern exactly:
   - `RefreshInterval = 10 * time.Minute` constant
   - `Controller` struct with `quotaProvider quota.Provider` field
   - `NewController(quotaProvider quota.Provider) *Controller`
   - `Reconcile(ctx)`: calls `quotaProvider.Update(ctx)`, returns `RequeueAfter: RefreshInterval`
   - `Register()`: uses `singleton.Source()` + `singleton.AsReconciler(c)`, named `"quota"`

2. **Register controller** in `pkg/controllers/controllers.go`
   - Add import for `quotacontroller "github.com/Azure/karpenter-provider-azure/pkg/controllers/quota"`
   - Add `quotaProvider quota.Provider` parameter to `NewControllers()`
   - Add `quotacontroller.NewController(quotaProvider)` to returned controllers slice

3. **Create and wire quota provider** in `pkg/operator/operator.go`
   - Import quota provider package
   - Create `quotaProvider` using `quota.NewProvider(azClient.UsageClient(), azConfig.Location)`
   - Add `QuotaProvider` field to `Operator` struct
   - Assign in return statement

4. **Pass to controllers** in `cmd/controller/main.go`
   - Add `op.QuotaProvider` parameter to `controllers.NewControllers()` call

### Tests
- **Add quota controller tests** in `pkg/controllers/quota/suite_test.go`
  - Follow `pkg/controllers/instancetype/suite_test.go` pattern
  - Create `test.Environment`, use quota provider wired with fake `UsageAPI`
  - Test successful reconciliation updates quota data
  - Test reconciliation returns `RequeueAfter: 10m`
  - Test pager startup error from usage API propagates

### Files to create/modify
- `pkg/controllers/quota/controller.go` — new file: controller implementation
- `pkg/controllers/quota/suite_test.go` — new file: controller tests
- `pkg/controllers/controllers.go` — add quota controller registration
- `pkg/operator/operator.go` — add QuotaProvider to Operator, create it in NewOperator
- `cmd/controller/main.go` — pass QuotaProvider to NewControllers
- `cmd/controller/main_ccp.go` — if exists with similar wiring, update too

### Verification
- `go build ./...` compiles
- `go test ./pkg/controllers/quota/...` passes
- `go test ./...` — all existing tests still pass

---

## Relevant Files

- `pkg/providers/azclient/azclient.go` — add UsageAPI interface, AZClient field, constructors
- `pkg/fake/usageapi.go` — **new**: fake UsageAPI (follow communityimageversionsapi.go pattern)
- `pkg/fake/types.go` — reference for MockedFunction/AtomicPtrSlice patterns
- `pkg/providers/quota/quota.go` — **new**: quota provider
- `pkg/controllers/quota/controller.go` — **new**: quota controller (follow instancetype/controller.go)
- `pkg/controllers/controllers.go` — register quota controller
- `pkg/operator/operator.go` — create QuotaProvider, add to Operator struct
- `cmd/controller/main.go` — pass QuotaProvider to NewControllers
- `pkg/test/environment.go` — add fake UsageAPI and QuotaProvider
- `pkg/controllers/instancetype/controller.go` — **reference**: singleton controller pattern
- `pkg/fake/communityimageversionsapi.go` — **reference**: simple list pager fake pattern
- `pkg/providers/pricing/pricing.go` — **reference**: provider with RWMutex + ChangeMonitor pattern

## Decisions
- Provider uses in-memory map with RWMutex (no go-cache TTL), since the controller manages refresh timing — follows pricing provider pattern
- Quota data keyed by `name.value` (e.g., `"standardBSFamily"`, `"cores"`) from the Azure Usage API
- Provider refresh is atomic at the cache level: build a fresh map first, then swap it in only after a full successful refresh; failed refreshes preserve the previous cache contents
- Controller uses 10-minute refresh interval as specified
- Singleton/periodic controller pattern (not event-driven) — same as instancetype controller
- `armcompute/v7` already in go.mod, no new dependency needed
- Fake uses single-page pagination (consistent with all other fakes) and only needs to simulate failure on the initial fetch that starts pagination
- `cmd/controller/main_ccp.go` has identical `NewControllers()` wiring as `main.go` — all changes to `main.go` must also be applied to `main_ccp.go`

## Further Considerations
1. **Integration with instance type selection**: The quota provider could eventually be used by the instance type provider to filter out instance types from families that are at quota. This is out of scope for the initial implementation but worth noting as a future enhancement.

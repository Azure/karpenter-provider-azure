# BYOK Disk Encryption Debugging Analysis

## Section 1: Executive Summary

The BYOK (Bring Your Own Key) implementation for Azure Disk Encryption in Karpenter allows customers to use their own encryption keys for OS and data disk encryption at rest. The challenge being investigated involves a test scenario where a Pod never becomes healthy after NodePool and Pod creation, despite the BYOK configuration being properly specified.

This document analyzes three critical areas:
1. **Code Path Dependency Tree**: How the `NODE_OSDISK_DISKENCRYPTIONSET_ID` environment variable flows from options loading through the Azure API
2. **Root Cause Analysis**: Seven layers where failures could prevent pod health (from configuration through pod readiness)
3. **Debugging Improvements**: Strategic logging, test tracing with Ginkgo `By()` statements, and test organization

---

## Section 2: Disk Encryption Set Code Path Dependency Tree

### Complete Flow from Options to Azure API

```
┌─ Configuration Loading Layer
│
├─ pkg/operator/options/options.go
│  └─ Options.DiskEncryptionSetID field declared (Line 94)
│
├─ pkg/operator/options/options.go
│  └─ AddFlags() registers "node-osdisk-diskencryptionset-id" flag
│     └─ Reads from NODE_OSDISK_DISKENCRYPTIONSET_ID env var (Line 122)
│
└─ pkg/operator/options/options.go
   └─ InitializeOperator() retrieves options via context

┌─ Operator Initialization Layer
│
└─ pkg/operator/operator.go
   └─ NewDefaultVMProvider() called with options.DiskEncryptionSetID
      ├─ azClient: AZClient for Azure resource operations
      ├─ instanceTypeProvider: For VM SKU details
      ├─ launchTemplateProvider: For OS/networking config
      ├─ loadBalancerProvider: For LB integration
      ├─ networkSecurityGroupProvider: For NSG config
      ├─ location: Region info
      ├─ resourceGroup: Target resource group
      ├─ subscriptionID: Azure subscription
      ├─ provisionMode: Bootstrapping mode (BootstrappingClient/Scriptless)
      └─ diskEncryptionSetID: THE CRITICAL PARAMETER (Line 221)

┌─ Provider Instance Layer
│
└─ pkg/providers/instance/vminstance.go
   └─ DefaultVMProvider struct holds diskEncryptionSetID as field (Line 143)
      ├─ location: string
      ├─ azClient: *AZClient
      ├─ instanceTypeProvider: instancetype.Provider
      ├─ launchTemplateProvider: *launchtemplate.Provider
      ├─ loadBalancerProvider: *loadbalancer.Provider
      ├─ networkSecurityGroupProvider: *networksecuritygroup.Provider
      ├─ resourceGroup: string
      ├─ subscriptionID: string
      ├─ provisionMode: string
      ├─ diskEncryptionSetID: string ← STORED HERE (Line 143)
      └─ errorHandling: *offerings.ResponseErrorHandler

┌─ CloudProvider Entry Point
│
└─ pkg/cloudprovider/cloudprovider.go
   └─ Create(ctx, nodeClaim) entry point
      ├─ Validates NodeClass (AKSNodeClass) is ready
      ├─ Resolves instance types based on requirements
      ├─ Calls createVMInstance()
      │
      └─ createVMInstance() at Line 155
         └─ Calls c.vmInstanceProvider.BeginCreate()
            └─ Delegates to DefaultVMProvider.BeginCreate()

┌─ VM Launch Orchestration Layer
│
└─ pkg/providers/instance/vminstance.go
   └─ BeginCreate(ctx, nodeClass, nodeClaim, instanceTypes)
      ├─ Calls offerings.PickSkuSizePriorityAndZone() for instance selection
      ├─ Calls getLaunchTemplate() for OS/networking config
      ├─ Calls loadBalancerProvider.LoadBalancerBackendPools()
      ├─ Calls createNetworkInterface() → creates NIC via Azure API
      │
      └─ Calls beginLaunchInstance() → THE ORCHESTRATION FUNCTION

┌─ Instance Launch Orchestration
│
└─ pkg/providers/instance/vminstance.go
   └─ beginLaunchInstance(ctx, nodeClass, nodeClaim, instanceTypes)
      ├─ Line 696: Picks best SKU/zone from available instance types
      ├─ Line 698: Gets launch template with getLaunchTemplate()
      ├─ Line 708: Generates resource name for VM/disk/NIC
      ├─ Line 710: Calls loadBalancerProvider.LoadBalancerBackendPools()
      ├─ Line 713: Gets network plugin settings from options context
      ├─ Line 716: Checks if VNET is AKS-managed
      ├─ Line 727: Creates network interface via createNetworkInterface()
      │
      └─ Line 745: Calls createVirtualMachine() with populated createVMOptions
         └─ This is where DiskEncryptionSetID is passed to VM creation

┌─ VM Creation Options Assembly
│
└─ pkg/providers/instance/vminstance.go
   └─ createVMOptions struct populated at Line 745-763
      ├─ VMName: resourceName
      ├─ NicReference: nicReference (from network interface)
      ├─ Zone: zone (from offerings)
      ├─ CapacityType: capacityType (On-Demand or Spot)
      ├─ Location: p.location
      ├─ SSHPublicKey: options.SSHPublicKey
      ├─ LinuxAdminUsername: options.LinuxAdminUsername
      ├─ NodeIdentities: options.NodeIdentities
      ├─ NodeClass: nodeClass (AKSNodeClass)
      ├─ LaunchTemplate: launchTemplate
      ├─ InstanceType: instanceType
      ├─ ProvisionMode: p.provisionMode
      ├─ UseSIG: options.UseSIG
      ├─ DiskEncryptionSetID: p.diskEncryptionSetID ← CRITICAL LINE 763
      └─ NodePoolName: nodeClaim labels

┌─ Azure API VM Creation
│
└─ pkg/providers/instance/vminstance.go
   └─ createVirtualMachine(ctx, opts *createVMOptions)
      ├─ Line 650: Calls newVMObject(opts) to construct VM resource
      ├─ Line 668: Calls azClient.virtualMachinesClient.BeginCreateOrUpdate()
      ├─ Line 674: Polls until VM creation completes
      │
      └─ VM object creation is critical

┌─ VM Object Creation (CRITICAL FOR ENCRYPTION)
│
└─ pkg/providers/instance/vminstance.go
   └─ newVMObject(opts *createVMOptions) creates armcompute.VirtualMachine
      ├─ Line 563: Calls setVMPropertiesOSDiskType()
      │  └─ Sets Caching/DiffDisk options if ephemeral
      │
      ├─ Line 564: Calls setVMPropertiesOSDiskEncryption(vm.Properties, opts.DiskEncryptionSetID)
      │  └─ THE CRITICAL ENCRYPTION SETUP FUNCTION
      │
      ├─ Line 565: Calls setImageReference()
      ├─ Line 566: Calls setVMPropertiesBillingProfile()
      ├─ Line 567: Calls setVMPropertiesSecurityProfile()
      └─ Line 569-573: Sets CustomData for provisioning

┌─ DISK ENCRYPTION APPLICATION (CRITICAL)
│
└─ pkg/providers/instance/vminstance.go
   └─ setVMPropertiesOSDiskEncryption(vmProperties *armcompute.VirtualMachineProperties, diskEncryptionSetID string)
      ├─ Line 590: Checks if diskEncryptionSetID != ""
      │
      ├─ IF TRUE:
      │  ├─ Line 591: Ensures OSDisk.ManagedDisk exists (allocates if nil)
      │  ├─ Line 594: Sets ManagedDisk.DiskEncryptionSet
      │  └─ Line 595: Sets DiskEncryptionSet.ID = diskEncryptionSetID ← ENCRYPTION SET ID APPLIED HERE
      │
      └─ IF FALSE:
         └─ No encryption is applied (uses default Microsoft-managed keys)

      **Azure API Impact:**
      └─ When BeginCreateOrUpdate() is called with this VM object,
         Azure reads the DiskEncryptionSet.ID and:
         ├─ Validates DES exists and is accessible
         ├─ Validates Karpenter identity has DES Reader role
         ├─ Validates Karpenter identity has Key Vault Crypto permissions
         ├─ Creates OS disk encrypted with the DES's referenced key
         └─ Returns error if any validation fails (sync-side error)

┌─ VirtualMachine Promise Polling
│
└─ pkg/providers/instance/vminstance.go
   └─ vmPromise.Wait() polls for VM completion
      ├─ Async provisioning errors caught here
      ├─ If encryption fails during provisioning:
      │  └─ Error surfaces during promise polling
      │
      └─ VM is marked Ready when provisioning completes
         ├─ Kubelet bootstrap begins
         └─ Node joins cluster

┌─ Node Bootstrap and Kubelet Startup
│
└─ CustomData bootstrap scripts that:
   ├─ Install container runtime (containerd)
   ├─ Configure network plugins
   ├─ Start kubelet service
   ├─ Join cluster with bootstrap token
   └─ Register node with API server

┌─ Node Registration and Pod Scheduling
│
└─ Kubernetes Control Plane
   ├─ Node registers with API server
   ├─ Node becomes "Ready" after kubelet startup
   ├─ Pod is scheduled to node
   ├─ Pod container image pulled
   ├─ Pod starts
   └─ Pod readiness probes checked

┌─ Test Verification Flow
│
└─ test/suites/byok/suite_test.go
   └─ BYOK Integration Test
      ├─ Line 71: CreateKeyVaultAndDiskEncryptionSet() sets up Azure resources
      ├─ Line 72: ExpectSettingsOverridden() sets NODE_OSDISK_DISKENCRYPTIONSET_ID env var
      ├─ Line 74: Creates AKSNodeClass with DefaultAKSNodeClass()
      ├─ Line 75: Creates NodePool with DefaultNodePool()
      ├─ Line 77: Creates test Pod
      ├─ Line 78: ExpectCreated() applies resources to Kubernetes
      ├─ Line 79: EventuallyExpectHealthyWithTimeout(pod, 15 minutes)
      │  └─ WAITING FOR POD TO BECOME HEALTHY
      ├─ Line 80: ExpectCreatedNodeCount("==", 1) verifies exactly 1 node created
      ├─ Line 82: env.GetVM() retrieves VM properties from Azure
      ├─ Line 83-89: Validates VM.Properties exist
      ├─ Line 90-91: Validates OSDisk.ManagedDisk exists
      ├─ Line 92-93: Validates ManagedDisk.DiskEncryptionSet exists
      ├─ Line 94-96: Validates DiskEncryptionSet.ID matches expected value
      └─ IF ALL PASS: Test succeeds; IF ANY FAIL or POD NOT HEALTHY: Test fails
```

---

## Section 3: Root Cause Analysis - Why Pod Never Becomes Healthy

### Layer-by-Layer Failure Scenarios

#### 1. Configuration Layer - DES ID Not Set/Invalid

| **Failure Point** | **Symptoms** | **Detection Method** |
|---|---|---|
| NODE_OSDISK_DISKENCRYPTIONSET_ID env var not set | Pod created but node never joins; VM disk encrypted with Microsoft-managed keys instead of customer-managed | Check: `echo $NODE_OSDISK_DISKENCRYPTIONSET_ID` in controller pod; check Options.DiskEncryptionSetID is empty |
| NODE_OSDISK_DISKENCRYPTIONSET_ID malformed | VM creation fails with BadRequest; error in karpenter logs | Check: DES ID format `/subscriptions/{subId}/resourceGroups/{rg}/providers/Microsoft.Compute/diskEncryptionSets/{desName}` |
| NODE_OSDISK_DISKENCRYPTIONSET_ID points to non-existent DES | VM creation fails; Azure returns NotFound error during SetProperties call | Check: `az disk-encryption-set show -g {rg} -n {desName}` exists and is accessible |
| NODE_OSDISK_DISKENCRYPTIONSET_ID not set in AKS addon mode | Pod fails health checks if BYOK was expected but not applied | Check: Container startup logs for env var configuration; managed cluster properties for NodeOSDiskDiskEncryptionSetId |
| Managed cluster has DES ID but env var not set | Karpenter ignores cluster's DES ID; uses Microsoft-managed keys instead | Check: `az aks show -g {rg} -n {clusterName} --query nodeOSDiskDiskEncryptionSetId`; compare to controller env var |

**Debugging**: Add logging at Options.AddFlags() to verify flag is loaded; add check for whether env var matches cluster properties

---

#### 2. Provider Layer - DES ID Not Passed Through

| **Failure Point** | **Symptoms** | **Detection Method** |
|---|---|---|
| NewDefaultVMProvider() not called with DES ID | diskEncryptionSetID field remains empty in provider | Check: operator.go line 221 passes `options.FromContext(ctx).DiskEncryptionSetID` |
| DefaultVMProvider.diskEncryptionSetID field is nil/empty | DES ID lost during provider initialization | Inspect: vminstance.go DefaultVMProvider struct line 143 |
| DES ID lost between provider init and BeginCreate() | diskEncryptionSetID becomes empty before VM creation | Add logging at BeginCreate() entry to verify field value |

**Debugging**: Add logging at NewDefaultVMProvider() to verify diskEncryptionSetID parameter

---

#### 3. VM Creation Layer - Encryption Not Applied

| **Failure Point** | **Symptoms** | **Detection Method** |
|---|---|---|
| createVMOptions.DiskEncryptionSetID not populated | VM created without encryption; disk uses default Microsoft-managed keys | Check: createVMOptions assembly at line 763 |
| newVMObject() called without DES ID in options | VM object created without DiskEncryptionSet in StorageProfile | Verify: Line 564 setVMPropertiesOSDiskEncryption() call passes opts.DiskEncryptionSetID |
| setVMPropertiesOSDiskEncryption() receives empty string | Function skips encryption setup (Line 590 condition fails) | Add logging at setVMPropertiesOSDiskEncryption() entry |
| setVMPropertiesOSDiskEncryption() fails to set ManagedDisk | OSDisk properties remain nil/incomplete | Check: vmProperties.StorageProfile.OSDisk.ManagedDisk allocation at line 591 |

**Debugging**: Add logging in newVMObject() and setVMPropertiesOSDiskEncryption()

---

#### 4. Azure API Layer - DES Validation Fails

| **Failure Point** | **Symptoms** | **Detection Method** |
|---|---|---|
| Azure rejects DES ID (not found) | VM creation fails with NotFound error; vmPromise.Wait() returns error | Check: CloudProvider logs for "creating instance failed" errors |
| Karpenter identity lacks DES Reader role | Azure returns Unauthorized/Forbidden; VM creation blocked | Check: RBAC role assignments: `az role assignment list --scope {desId}` |
| Karpenter identity lacks Key Vault Crypto permissions | VM creation fails; Azure can't access key in vault | Check: Key Vault access policies or RBAC: `az keyvault role assignment list --vault-name {kvName}` |
| DES key is disabled/deleted/expired | VM creation fails; key vault rejects unwrap operation | Check: Key status in vault: `az keyvault key list-versions --vault-name {kvName} --name {keyName}` |
| Network connectivity to Key Vault fails | VM creation hangs or times out during encryption setup | Check: Service endpoints, firewalls, NSG rules blocking Key Vault access |

**Debugging**: Add logging at createVirtualMachine() before/after BeginCreateOrUpdate(); capture Azure error responses

---

#### 5. Bootstrap/Kubelet Layer - Node Doesn't Join Cluster

| **Failure Point** | **Symptoms** | **Detection Method** |
|---|---|---|
| VM provisioning succeeds but OS fails to boot | VM exists in Azure but never reports Ready; kubelet never starts | Check: VM boot diagnostics in Azure portal; OS-level logs |
| Kubelet fails to start due to disk errors | Node registered but immediately becomes NotReady | Check: `kubectl describe node {nodeName}` for conditions; kubelet logs |
| Kubelet can't access encrypted disk | OS boots but file system errors occur | Check: dmesg/kernel logs in VM; disk encryption validation errors |
| Node registration TLS bootstrap fails | Kubelet starts but can't authenticate to API server | Check: Kubelet logs for bootstrap token errors |
| DNS/network issues prevent cluster communication | Kubelet can't reach API server endpoint | Check: Network NSG rules, routing, DNS resolution |

**Debugging**: Add logging at CustomData setup; check bootstrap script output in VM serial logs

---

#### 6. Pod Scheduling Layer - Pod Never Scheduled

| **Failure Point** | **Symptoms** | **Detection Method** |
|---|---|---|
| Node created but remains NotReady too long | Pod stays Pending beyond timeout; no node scheduling attempted | Check: `kubectl get nodes -o wide` and `kubectl get nodeclaims` |
| Node taints prevent pod scheduling | Node Ready but taints block pod; pod stays Pending | Check: `kubectl describe node {nodeName}` for taints; scheduler events |
| Resource requests exceed node capacity | Pod can't fit on node (even though it should) | Check: `kubectl top nodes` and pod resource requirements |
| Pod selector doesn't match any nodes | Pod stays Pending due to label/node selector mismatch | Check: Pod nodeSelector/affinity vs node labels |

**Debugging**: Add logging at test expectations for node creation/readiness; check Kubernetes events

---

#### 7. Pod Readiness Layer - Pod Fails Health Checks

| **Failure Point** | **Symptoms** | **Detection Method** |
|---|---|---|
| Container image pull fails | Pod stuck in ImagePullBackOff; can't start container | Check: `kubectl describe pod {podName}` for image pull errors |
| Container fails to start | Pod in CrashLoopBackOff or Error state | Check: `kubectl logs {podName}` for startup errors |
| Readiness probe fails persistently | Container running but readiness check fails continuously | Check: `kubectl describe pod {podName}` for probe failures |
| Liveness probe fails | Container killed due to failed liveness probe | Check: `kubectl logs --previous {podName}` for crash info |
| Pod can't access ClusterIP service | Application fails startup dependency checks | Check: Network connectivity from pod to services |

**Debugging**: Add logging at EventuallyExpectHealthyWithTimeout(); check pod conditions and events

---

## Section 4: Test Tracing Improvements

### Strategic Ginkgo By() Statements for Test Organization

#### **BEFORE: Current Test Structure**

```go
It("should provision a VM with customer-managed key disk encryption", func() {
    ctx := context.Background()
    var diskEncryptionSetID string
    if env.InClusterController {
        diskEncryptionSetID = CreateKeyVaultAndDiskEncryptionSet(ctx, env)
        env.ExpectSettingsOverridden(corev1.EnvVar{Name: "NODE_OSDISK_DISKENCRYPTIONSET_ID", Value: diskEncryptionSetID})
    }

    nodeClass := env.DefaultAKSNodeClass()
    nodePool := env.DefaultNodePool(nodeClass)

    pod := test.Pod()
    env.ExpectCreated(nodeClass, nodePool, pod)
    env.EventuallyExpectHealthyWithTimeout(pod, time.Minute*15)
    env.ExpectCreatedNodeCount("==", 1)

    vm := env.GetVM(pod.Spec.NodeName)
    // ... assertions ...
})
```

**Issues**: No phase visibility; single timeout masks failures; unclear which phase failed

---

#### **AFTER: Enhanced Test with By() Statements**

```go
It("should provision a VM with customer-managed key disk encryption", func() {
    ctx := context.Background()
    var diskEncryptionSetID string

    By("Phase 1: Setting up DES (Disk Encryption Set)")
    if env.InClusterController {
        diskEncryptionSetID = CreateKeyVaultAndDiskEncryptionSet(ctx, env)
        Expect(diskEncryptionSetID).NotTo(BeEmpty(), "DES ID should not be empty")
        env.ExpectSettingsOverridden(corev1.EnvVar{
            Name:  "NODE_OSDISK_DISKENCRYPTIONSET_ID",
            Value: diskEncryptionSetID,
        })
    }

    By("Phase 2: Creating NodeClass and NodePool")
    nodeClass := env.DefaultAKSNodeClass()
    Expect(nodeClass).NotTo(BeNil(), "NodeClass should be created")
    nodePool := env.DefaultNodePool(nodeClass)
    Expect(nodePool).NotTo(BeNil(), "NodePool should be created")

    By("Phase 3: Creating test Pod")
    pod := test.Pod()
    Expect(pod).NotTo(BeNil(), "Pod should be created")

    By("Phase 4: Applying resources to Kubernetes")
    env.ExpectCreated(nodeClass, nodePool, pod)

    By("Phase 5: Waiting for VM to be created")
    Eventually(func() int {
        return len(env.ExpectCreatedNodeCount("==", 1))
    }, time.Minute*3, time.Second*10).Should(Equal(1), "Exactly 1 node should be created")

    By("Phase 6: Waiting for node to become Ready")
    nodes := env.ExpectCreatedNodeCount("==", 1)
    Expect(len(nodes)).To(Equal(1))
    nodeName := nodes[0].Name

    Eventually(func() corev1.ConditionStatus {
        node := &corev1.Node{}
        env.Client.Get(ctx, client.ObjectKey{Name: nodeName}, node)
        return nodeutils.GetCondition(node, corev1.NodeReady).Status
    }, time.Minute*3, time.Second*5).Should(Equal(corev1.ConditionTrue), "Node should become Ready")

    By("Phase 7: Waiting for Pod to be scheduled and running")
    Eventually(func() corev1.PodPhase {
        podObj := &corev1.Pod{}
        env.Client.Get(ctx, client.ObjectKey{Name: pod.Name, Namespace: pod.Namespace}, podObj)
        return podObj.Status.Phase
    }, time.Minute*5, time.Second*5).Should(Equal(corev1.PodRunning), "Pod should reach Running phase")

    By("Phase 8: Waiting for Pod to become healthy (readiness probes pass)")
    env.EventuallyExpectHealthyWithTimeout(pod, time.Minute*10)

    By("Phase 9: Verifying VM disk encryption configuration")
    vm := env.GetVM(pod.Spec.NodeName)
    Expect(vm.Properties).ToNot(BeNil(), "VM properties should exist")
    Expect(vm.Properties.StorageProfile).ToNot(BeNil(), "StorageProfile should exist")
    Expect(vm.Properties.StorageProfile.OSDisk).ToNot(BeNil(), "OSDisk should exist")
    Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk).ToNot(BeNil(), "ManagedDisk should exist")
    Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet).ToNot(BeNil(), "DiskEncryptionSet should exist")
    Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet.ID).ToNot(BeNil(), "DES ID should exist")

    if env.InClusterController {
        Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet.ID)).
            To(Equal(diskEncryptionSetID), "VM DES ID should match configured DES ID")
    }
})
```

**Improvements**:
- **Clear Phase Visibility**: Each `By()` shows exactly where test is in lifecycle
- **Granular Timeouts**: Each phase has appropriate timeout (Phase 5: 3 min, Phase 8: 10 min)
- **Early Failure Detection**: Tests fail at specific phase, making debugging obvious
- **Intermediate Assertions**: Verifies state at each phase boundary
- **Logging Integration**: `By()` messages appear in test output for quick scanning

---

## Section 5: Logging Statement Recommendations

### Comprehensive Logging Table by Phase

| **Phase** | **File** | **Function** | **Location** | **Recommended Log Statement** | **Why It Helps** | **Addresses Scenario** |
|---|---|---|---|---|---|---|
| **1. Options Loading** | pkg/operator/options/options.go | `AddFlags()` | After flag registration | `log.Info("DiskEncryptionSetID flag registered", "value", env.WithDefaultString("NODE_OSDISK_DISKENCRYPTIONSET_ID", ""))` | Confirms flag was parsed from env var | 1.1 - Config not set |
| **1. Options Loading** | pkg/operator/options/options.go | `AddFlags()` | Line 122 | `log.Info("NODE_OSDISK_DISKENCRYPTIONSET_ID env var", "value", o.DiskEncryptionSetID)` | Shows env var value before parsing | 1.1 - Config not set |
| **1. Options Loading** | pkg/operator/options/options.go | `Parse()` | After options parsing | `log.Info("Options parsed", "diskEncryptionSetID", o.DiskEncryptionSetID)` | Verifies options available to operator | 1.1 - Config not propagating |
| **2. DES ID Provider Passing** | pkg/operator/operator.go | NewDefaultVMProvider call | Before function call | `log.Info("Initializing VMProvider with DES", "diskEncryptionSetID", options.FromContext(ctx).DiskEncryptionSetID)` | Confirms DES ID passed to provider | 2.2 - DES ID lost during init |
| **2. DES ID Provider Passing** | pkg/providers/instance/vminstance.go | `NewDefaultVMProvider()` | Function entry | `log.Info("NewDefaultVMProvider called", "diskEncryptionSetID", diskEncryptionSetID)` | Confirms parameter received | 2.2 - DES ID not passed |
| **2. DES ID Provider Passing** | pkg/providers/instance/vminstance.go | `NewDefaultVMProvider()` | After provider creation | `log.Info("VMProvider initialized", "diskEncryptionSetID", p.diskEncryptionSetID)` | Verifies field set in struct | 2.3 - DES ID lost between init and BeginCreate |
| **3. Instance Type & Launch Template** | pkg/providers/instance/vminstance.go | `BeginCreate()` | Function entry | `log.Info("BeginCreate called", "diskEncryptionSetID", p.diskEncryptionSetID)` | Confirms provider field still set | 3.1 - DES ID becomes empty before VM creation |
| **3. Instance Type & Launch Template** | pkg/providers/instance/vminstance.go | `beginLaunchInstance()` | After SKU selection | `log.Info("Instance selected for launch", "instanceType", instanceType.Name, "diskEncryptionSetID", p.diskEncryptionSetID)` | Verifies DES ID present during instance selection | 3.1 - DES ID lost during selection |
| **4. newVMObject and Encryption Setup** | pkg/providers/instance/vminstance.go | `createVirtualMachine()` | Function entry | `log.Info("createVirtualMachine called", "diskEncryptionSetID", opts.DiskEncryptionSetID)` | Confirms options passed to VM builder | 3.2 - DES ID not in createVMOptions |
| **4. newVMObject and Encryption Setup** | pkg/providers/instance/vminstance.go | `newVMObject()` | Before setVMPropertiesOSDiskEncryption | `log.Info("About to set OSDisk encryption", "diskEncryptionSetID", opts.DiskEncryptionSetID)` | Confirms parameter available before encryption function | 3.3 - DES ID not available |
| **4. newVMObject and Encryption Setup** | pkg/providers/instance/vminstance.go | `setVMPropertiesOSDiskEncryption()` | Function entry | `log.Info("setVMPropertiesOSDiskEncryption called", "diskEncryptionSetID", diskEncryptionSetID, "isEmpty", diskEncryptionSetID == "")` | Shows if DES ID is empty at encryption point | 3.3/3.4 - Encryption skipped; doesn't set ManagedDisk |
| **4. newVMObject and Encryption Setup** | pkg/providers/instance/vminstance.go | `setVMPropertiesOSDiskEncryption()` | After DiskEncryptionSet assignment | `log.Info("OSDisk encryption configured", "desID", vmProperties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet.ID)` | Verifies encryption config actually applied to VM object | 3.4 - ManagedDisk not allocated |
| **5. VM Creation Request** | pkg/providers/instance/vminstance.go | `createVirtualMachine()` | Before BeginCreateOrUpdate | `log.Info("Sending VM creation request to Azure", "vmName", opts.VMName, "desID", opts.DiskEncryptionSetID, "storageProfile", opts)` | Confirms VM object has encryption config before Azure API call | 4.0 - Azure validation before sending |
| **5. VM Creation Request** | pkg/providers/instance/vminstance.go | `createVirtualMachine()` | After BeginCreateOrUpdate call | `log.Info("VM creation LRO started", "vmName", opts.VMName, "operationID", poller.ID())` | Tracks Azure LRO for monitoring | 4.0 - Async Azure failures |
| **6. VM Creation Polling** | pkg/providers/instance/vminstance.go | `createVirtualMachine()` | In polling loop or after PollUntilDone | `log.Info("VM creation polling complete", "vmName", opts.VMName, "status", result.Properties.ProvisioningState)` | Shows polling progress | 4.3 - Key vault connectivity timeouts |
| **6. VM Creation Polling** | pkg/providers/instance/vminstance.go | `createVirtualMachine()` | On polling error | `log.Error("VM creation failed", "vmName", opts.VMName, "error", err)` | Captures Azure API error details (NotFound, Unauthorized, etc.) | 4.1/4.2/4.3 - Azure API failures |
| **7. Bootstrap and Kubelet** | pkg/providers/instance/vminstance.go | Bootstrap data generation | Before CustomData is set | `log.Info("CustomData prepared", "provisionMode", opts.ProvisionMode, "note", "DES configured in VM properties, not bootstrap")` | Notes that DES is not in bootstrap (it's in VM properties) | 5.0 - Node boot issues |
| **7. Bootstrap and Kubelet** | pkg/providers/instance/vminstance.go | `newVMObject()` | When setting CustomData | `log.Info("Setting CustomData for provisioning", "provisionMode", opts.ProvisionMode, "scriptLength", len(customData))` | Verifies provisioning mode configured | 5.0 - Kubelet startup failures |
| **8. Pod Health and Readiness** | test/suites/byok/suite_test.go | Test function | In pod health check loop | `By("Checking pod health: phase=" + string(pod.Status.Phase) + ", ready=" + strconv.FormatBool(isPodReady(pod)))` | Tracks pod readiness progress | 6.0/7.0 - Pod scheduling/readiness failures |
| **8. Pod Health and Readiness** | pkg/controllers/ | `PodController` | On pod update | `log.Info("Pod state changed", "podName", pod.Name, "phase", pod.Status.Phase, "conditions", pod.Status.Conditions)` | Monitors pod state transitions | 7.0 - Readiness probe failures |
| **9. Node Registration** | pkg/cloudprovider/cloudprovider.go | `GetInfo()` | On node state query | `log.Info("Node registered with cluster", "nodeName", node.Name, "ready", isReady)` | Tracks node lifecycle to Ready | 5.0/6.0 - Node registration delays |
| **9. Node Registration** | pkg/cloudprovider/cloudprovider.go | `Create()` | On NodeClaim creation completion | `log.Info("NodeClaim creation complete", "nodeName", nodeClaim.Status.NodeName, "vmName", vmName)` | Confirms VM is linked to Kubernetes node | 5.0/6.0 - Node registration failures |

---

## Section 6: Critical Path Analysis

### Code Sections by Failure Mode

#### **Pod Never Schedules (Most Critical Path)**

```
Options.DiskEncryptionSetID (LINE 94)
    ↓
AddFlags() registration (LINE 122)
    ↓
FromContext() retrieval in operator.go (LINE 210)
    ↓
NewDefaultVMProvider() call (LINE 221)
    ↓
DefaultVMProvider.diskEncryptionSetID field (LINE 143)
    ↓
BeginCreate() entry (LINE 189)
    ↓
beginLaunchInstance() (LINE 689)
    ↓
createVirtualMachine() (LINE 638)
    ↓
newVMObject() call (LINE 650)
    ↓
setVMPropertiesOSDiskEncryption() (LINE 589)
    ↓
VM object sent to Azure API (LINE 668)
    ↓
Azure validates DES and creates disk (ASYNC)
    ↓
vmPromise.Wait() polls for completion (LINE 674)
    ↓
Node registers with Kubernetes (async after VM ready)
    ↓
Pod scheduled to node
    ↓
Pod becomes healthy
```

**If ANY step fails**: Pod never schedules or becomes healthy.

---

#### **Disk Not Encrypted Path**

```
setVMPropertiesOSDiskEncryption() receives empty string (LINE 589)
    ↓ (condition: diskEncryptionSetID == "")
    ↓
Function returns early (no ManagedDisk allocation)
    ↓
VM created with default Microsoft-managed encryption
    ↓
Test fails at assertion: DiskEncryptionSet.ID is nil (LINE 93)
```

---

#### **Azure API Rejects DES Path**

```
newVMObject() creates VM with DES ID set (LINE 595)
    ↓
BeginCreateOrUpdate() sends to Azure (LINE 668)
    ↓
Azure validates DES exists
    ↓ (DES not found / identity lacks permissions)
    ↓
Azure returns error: NotFound / Unauthorized
    ↓
PollUntilDone() receives error (LINE 674)
    ↓
Error surfaced to CloudProvider.Create() (LINE 155)
    ↓
CreateError returned
    ↓
NodeClaim fails to launch
    ↓
Pod stays Pending
```

---

## Section 7: Current Implementation Limitations

### How Disk Encryption Set ID is Currently Obtained

**The ONLY source of DES ID today**:
- `NODE_OSDISK_DISKENCRYPTIONSET_ID` environment variable
- Read via `env.WithDefaultString()` in [`pkg/operator/options/options.go#L122`](pkg/operator/options/options.go)
- Set via `--node-osdisk-diskencryptionset-id` CLI flag

**What is NOT currently supported**:
- Reading DES ID from managed cluster's properties (`ManagedCluster.Properties.NodeOSDiskDiskEncryptionSetID`)
- Per-NodePool or per-Node overrides (AKSNodeClass does not have `DiskEncryptionSetID` field)
- Automatic discovery of DES ID from cluster configuration
- Drift detection for DES changes

---

### Why Test Fails with Managed Karpenter

When running in **managed Karpenter** (AKS addon mode):

1. **Environment variable not set**:
   - In managed addon mode, the `NODE_OSDISK_DISKENCRYPTIONSET_ID` env var may not be configured
   - Test checks: `if env.InClusterController` to decide whether to create and set DES
   - In AKS addon mode, this conditional is false, so env var is never set
   - Result: `options.DiskEncryptionSetID` remains empty string

2. **Empty DES ID flows through entire provisioning**:
   - Empty string passed to `NewDefaultVMProvider()` → `p.diskEncryptionSetID = ""`
   - Empty string passed through `createVMOptions`
   - [`setVMPropertiesOSDiskEncryption()`](pkg/providers/instance/vminstance.go#L589) receives empty string
   - Condition `diskEncryptionSetID != ""` at line 590 evaluates to FALSE
   - Encryption setup is skipped entirely
   - VM created with Microsoft-managed keys (not customer-managed)

3. **Pod never becomes healthy because**:
   - If test expects DES to be applied but it's not → test fails at assertion
   - If test doesn't validate DES but relies on it for other functionality → pod may fail health checks due to missing encryption setup

---

### Managed Cluster DES ID Not Being Used

Even if a managed cluster has `DiskEncryptionSetID` set in its properties:
- Karpenter has **NO code** to read this property
- AKSClient doesn't query the cluster properties at startup
- No fallback to cluster-level DES if env var is empty
- The information is simply ignored

**To use cluster's DES ID, you would need to**:
1. Query the managed cluster: `az aks show -g {rg} -n {clusterName}`
2. Extract `nodeOSDiskDiskEncryptionSetId` from properties
3. Pass it to Karpenter via env var (since it's the only path currently supported)

---

## Section 8: Next Steps for Debugging

### Step-by-Step Debugging Checklist

#### **Step 1: Verify Configuration Layer**

- [ ] Confirm env var is set: `kubectl get deployment karpenter -n karpenter -o yaml | grep NODE_OSDISK_DISKENCRYPTIONSET_ID`
- [ ] Check env var value is non-empty: `echo $NODE_OSDISK_DISKENCRYPTIONSET_ID` inside controller pod
- [ ] Validate DES ID format: Should be `/subscriptions/{subId}/resourceGroups/{rg}/providers/Microsoft.Compute/diskEncryptionSets/{desName}`
- [ ] Confirm DES exists in Azure: `az disk-encryption-set show -g {rg} -n {desName}`

---

#### **Step 2: Verify Provider Initialization**

- [ ] Add log at NewDefaultVMProvider(): Log `diskEncryptionSetID` parameter to NewDefaultVMProvider
- [ ] Add log at NewDefaultVMProvider(): Log `diskEncryptionSetID` after provider creation
- [ ] Verify field value through entire lifecycle: Enable debug logging in DefaultVMProvider

---

#### **Step 3: Trace VM Creation Path**

- [ ] Add log at BeginCreate(): Log `p.diskEncryptionSetID` on BeginCreate() entry
- [ ] Add log at beginLaunchInstance(): Log `p.diskEncryptionSetID` when creating createVMOptions
- [ ] Add log at newVMObject(): Log `opts.DiskEncryptionSetID` in newVMObject()

---

#### **Step 4: Verify Encryption Application**

- [ ] Add log at setVMPropertiesOSDiskEncryption(): Log diskEncryptionSetID parameter and condition result
- [ ] Add log at setVMPropertiesOSDiskEncryption(): Log after `DiskEncryptionSet` assignment
- [ ] Check if condition `diskEncryptionSetID != ""` evaluates to true
- [ ] If false, DES ID is empty → investigate steps 1-3

---

#### **Step 5: Monitor Azure API Call**

- [ ] Add log at createVirtualMachine(): Log complete VM object before BeginCreateOrUpdate()
- [ ] Include in log: `vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet.ID`
- [ ] Check returned error from BeginCreateOrUpdate()
- [ ] If error: Decode Azure error details
  - NotFound: DES doesn't exist
  - Unauthorized/Forbidden: Identity lacks roles
  - Other: Capture full error message

---

#### **Step 6: Check RBAC Permissions**

- [ ] Get Karpenter identity: `kubectl get serviceaccount -n karpenter karpenter -o yaml`
- [ ] Check identity AAD mapping: `az identity show -g {rg} -n {identityName}`
- [ ] Verify DES Reader role: `az role assignment list --scope {desId} --query "[?properties.principalId=='{identityOid}']"`
- [ ] Verify Key Vault Crypto role: `az keyvault role assignment list --vault-name {kvName}`
- [ ] If roles missing: Assign them before running test

---

#### **Step 7: Verify Key Vault State**

- [ ] Check key status: `az keyvault key show --vault-name {kvName} --name {keyName}`
- [ ] Look for `enabled: true` and no expiration in past
- [ ] Check DES key reference: `az disk-encryption-set show -g {rg} -n {desName}`
- [ ] Verify it points to correct key in correct vault

---

#### **Step 8: Monitor VM Provisioning**

- [ ] Watch VM creation in Azure portal or CLI: `az vm show -g {rg} -n {vmName} --query "provisioningState"`
- [ ] Check boot diagnostics in Azure portal for OS-level errors
- [ ] Once VM is running, SSH into it: `ssh -i {key} azureuser@{vmPublicIP}`
  - [ ] Check disk is mounted: `lsblk` or `mount | grep /`
  - [ ] No mount errors → disk encryption working
  - [ ] Check kubelet is running: `systemctl status kubelet`
  - [ ] Check kubelet logs: `journalctl -u kubelet | tail -100`

---

#### **Step 9: Verify Node and Pod State**

- [ ] Check if node registered: `kubectl get nodes`
- [ ] If not found: Kubelet not joining cluster
  - Check kubelet logs from Step 8
  - Look for bootstrap token errors
  - Verify API server connectivity from VM

- [ ] If node found but NotReady:
  - `kubectl describe node {nodeName}` → check conditions
  - `kubectl logs -n karpenter -l app=karpenter | grep {nodeName}` → controller logs

- [ ] If node Ready but pod Pending:
  - `kubectl describe pod {podName}` → scheduler events
  - Check node taints: `kubectl describe node {nodeName}`

- [ ] If pod Running but not Ready:
  - `kubectl logs {podName}` → app logs
  - `kubectl describe pod {podName}` → readiness probe failures

---

#### **Step 10: Run Test with Enhanced Logging**

- [ ] Implement logging statements from Section 5
- [ ] Run test with debug logging enabled: `LOG_LEVEL=debug`
- [ ] Capture logs from:
  - Karpenter controller: `kubectl logs -n karpenter -l app=karpenter`
  - Test logs (if running locally)
  - Azure SDK logs (if enabled)
- [ ] Search logs for "setVMPropertiesOSDiskEncryption" and "diskEncryptionSetID"
- [ ] Trace flow from phase to phase looking for empty values or errors

---

#### **Step 11: Validate Test Assertions**

- [ ] Add Ginkgo `By()` statements from Section 4
- [ ] Run test with enhanced tracing
- [ ] Each `By()` will show exactly where test fails
- [ ] If test fails at Phase 5 (VM creation): Azure API issue (step 5-7)
- [ ] If test fails at Phase 6 (Node Ready): Kubelet/bootstrap issue (step 8)
- [ ] If test fails at Phase 8 (Pod healthy): Pod readiness issue (step 9)

---

#### **Step 12: Cross-Reference Current Code Implementation**

- [ ] Verify that only `NODE_OSDISK_DISKENCRYPTIONSET_ID` env var is the source (no cluster property fallback)
- [ ] Confirm AKSNodeClass does not have `DiskEncryptionSetID` field (per-node overrides not supported)
- [ ] Check if managed cluster properties are being read at startup (they should not be, if this is a gap)
- [ ] Identify whether env var is set in managed addon mode or if that's the test setup issue

---

### Quick Diagnostic Command

```bash
# Run test with full debugging
LOG_LEVEL=debug \
NODE_OSDISK_DISKENCRYPTIONSET_ID="/subscriptions/{subId}/resourceGroups/{rg}/providers/Microsoft.Compute/diskEncryptionSets/{desName}" \
go test -v -timeout 30m ./test/suites/byok -run "should provision a VM with customer-managed key"

# Monitor test progress
kubectl logs -f -n karpenter -l app=karpenter | grep -i "encryption\|des\|disk"

# Check test pod health in real-time
watch -n 2 'kubectl get pods -o wide && kubectl get nodes && kubectl get nodeclaims'
```

---

## References

- **Options Configuration**: `pkg/operator/options/options.go`
- **VM Provider**: `pkg/providers/instance/vminstance.go`
- **CloudProvider**: `pkg/cloudprovider/cloudprovider.go`
- **BYOK Test Suite**: `test/suites/byok/suite_test.go`
- **Azure Documentation**: [AKS Customer-Managed Keys](https://learn.microsoft.com/en-us/azure/aks/azure-disk-customer-managed-keys)

---

## Section 9: Test Execution Analysis - Managed Karpenter Failure

### Observed Failure Pattern

When running the test against managed Karpenter, the following occurs:

1. **Pod Creation**: Pod is created successfully and enters `Pending` phase
2. **Node Scheduling Failure**: Pod cannot be scheduled: `0/3 nodes are available: 3 node(s) had untolerated taint {CriticalAddonsOnly: }`
3. **No NodeClaim Created**: Despite pod being unschedulable, no new NodeClaim is created for provisioning
4. **Test Fails Immediately**: Phase 5 fails within 60ms - no time for Karpenter to act
5. **Pod Cleanup**: Pod is deleted during AfterEach

### Root Cause: Resource Creation Order

**The critical issue**: NodePool is created before NodeClass exists

In Phase 4, the test does:
```go
env.ExpectCreated(nodeClass, nodePool, pod)
```

This attempts to create all three simultaneously. However:
- `nodePool` references `nodeClass`
- When `nodePool` is created before `nodeClass` is persisted, the reference becomes invalid
- Karpenter cannot resolve the NodeClass for the NodePool
- No NodeClaims are created because the NodePool is unresolvable

**Evidence from earlier test runs** (before phase reorganization):
```
Failed resolving NodeClass
AKSNodeClass.karpenter.azure.com "roachgrass-1-hhfpvjsbmu" not found
```

This demonstrates the NodePool is trying to reference a NodeClass that doesn't exist in the cluster yet.

### Solution: Sequential Resource Creation

**Reorder Phase 4 to create resources separately**:

1. Create NodeClass first and wait for it
2. Create NodePool second (which now has the NodeClass available)
3. Create Pod third (which can now be scheduled)

This ensures proper dependency resolution in managed Karpenter environments.

### Recommended Test Changes

**Change Phase 4 from**:
```go
By("Phase 4: Applying resources to Kubernetes")
env.ExpectCreated(nodeClass, nodePool, pod)
```

**To**:
```go
By("Phase 4: Applying NodeClass to Kubernetes")
env.ExpectCreated(nodeClass)
By("NodeClass created successfully")

By("Phase 4b: Applying NodePool to Kubernetes")
env.ExpectCreated(nodePool)
By("NodePool created and linked to NodeClass")

By("Phase 4c: Applying Pod to Kubernetes")
env.ExpectCreated(pod)
By("Pod created and ready for scheduling")
```

This explicit sequencing:
- Ensures NodeClass exists before NodePool references it
- Allows Karpenter time to reconcile NodePool with its NodeClass reference
- Gives scheduler time to create NodeClaim for unschedulable pods
- Makes the test more robust across different environments

### Additional Logging to Detect This Issue

Add logging to the test to verify resource state at each step:

```go
By("Verifying NodeClass is available in cluster")
retrievedNodeClass := &v1beta1.AKSNodeClass{}
Expect(env.Client.Get(ctx, client.ObjectKey{Name: nodeClass.Name, Namespace: nodeClass.Namespace}, retrievedNodeClass)).To(Succeed(), "NodeClass should be retrievable from cluster")

By("Verifying NodePool is linked to NodeClass")
retrievedNodePool := &karpv1.NodePool{}
Expect(env.Client.Get(ctx, client.ObjectKey{Name: nodePool.Name, Namespace: nodePool.Namespace}, retrievedNodePool)).To(Succeed(), "NodePool should be retrievable from cluster")
Expect(retrievedNodePool.Spec.Template.Spec.NodeClassRef.Name).To(Equal(nodeClass.Name), "NodePool should reference correct NodeClass")

By("Checking for NodeClaims after pod creation")
nodeClaims := &karpv1.NodeClaimList{}
Expect(env.Client.List(ctx, nodeClaims)).To(Succeed(), "Should be able to list NodeClaims")
Expect(len(nodeClaims.Items)).To(BeGreaterThan(0), "At least one NodeClaim should be created for pending pod")
```

These checks verify:
- NodeClass is actually persisted in the cluster
- NodePool's reference to NodeClass is correct
- Karpenter has created a NodeClaim to handle the pending pod

### Why This Matters for Managed Karpenter

Managed Karpenter has stricter validation and reconciliation timing:
- Resources must exist in cluster API before dependent resources reference them
- Self-hosted Karpenter may tolerate temporary reference inconsistencies
- Managed addon enforces proper dependency chains
- Test must respect the same ordering as production scenarios

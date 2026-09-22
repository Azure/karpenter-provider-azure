# AKS Machine Cleanup When the VM Is Deleted

## Problem

An AKS Machine can continue to exist after its underlying virtual machine has been deleted, such as
after a spot eviction. The existing instance garbage collector only deletes a cloud instance when
there is no matching Kubernetes `NodeClaim` and the instance is older than five minutes. A stale
AKS Machine therefore remains indefinitely while its `NodeClaim` still exists.

## Design

The existing instance garbage collector remains the single cleanup path:

1. The AKS Machines SDK client adds `$expand=instanceView` to Machine list requests through a
   per-call Azure SDK policy. The generated SDK does not yet expose this REST option.
2. `BuildNodeClaimFromAKSMachine` copies `Machine.Properties.Status.VMState` to the synthetic cloud
   `NodeClaim` annotation `karpenter.azure.com/aks-machine-vm-state` when the state is present.
3. The instance garbage collector deletes the cloud instance when the annotation value is exactly
   `Deleted`, regardless of whether a matching cluster `NodeClaim` exists or whether the five-minute
   creation grace period has elapsed.
4. All other instances retain the existing rule: delete only when no cluster `NodeClaim` has the
   provider ID and the cloud instance is older than five minutes.
5. The existing cleanup helper deletes the AKS Machine and then deletes any Kubernetes `Node` with
   the same provider ID. The normal Karpenter lifecycle subsequently cleans up the cluster
   `NodeClaim`.

The deleted-VM override also bypasses the garbage collector's filter for cloud instances already
marked as deleting. This allows stale Kubernetes Nodes to be cleaned up while Machine deletion is
in progress without changing the idempotent provider delete behavior.

## Scope

- The behavior applies to both `aksmachineapi` and `aksmachineapiheaderbatch`; they share the same
  Machine list, conversion, and deletion paths.
- VM-based provisioning modes are unchanged because their synthetic cloud `NodeClaim`s do not carry
  the AKS Machine VM-state annotation.
- NAP and self-hosted deployments use the same provider and garbage-collection paths.

## Safety and Compatibility

- Garbage collection compares the annotation value with the Azure SDK `VMStateDeleted` enum rather
  than treating annotation presence as deletion.
- A missing VM state preserves existing behavior.
- The annotation is derived from Azure state and is not accepted as user input for an Azure request.
- Existing ownership and age protections remain unchanged for running Machines and Azure VMs.
- The list policy is scoped to `GET` requests for the Machine collection, including continuation
  pages. It does not modify Machine GET or create requests.

## Operational Considerations

`BuildNodeClaimFromAKSMachine` still requires the Machine response to include status, VM size, VM
resource ID, node image version, and creation timestamp. Live validation should confirm those fields
remain populated for `VMState=Deleted`. If the service removes any required field in that state, the
conversion needs a reduced deleted-Machine path so one stale Machine cannot fail the entire provider
list.

The custom `$expand` policy should be removed once the generated Azure SDK exposes the option
directly.

## Verification

- Verify the request policy preserves `api-version` and continuation parameters while adding
  `$expand=instanceView`.
- Verify missing, running, and deleted VM states are converted correctly.
- Verify a recent deleted Machine is removed even with a matching cluster `NodeClaim`.
- Verify running Machines with matching cluster `NodeClaim`s remain.
- Exercise the behavior against a live AKS Machine whose underlying VM has been deleted to validate
  the expanded response shape and AgentPool deletion semantics.

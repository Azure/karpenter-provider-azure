#!/usr/bin/env bash
# scaffold-pool-feature.sh — Generate boilerplate for a new pool-level feature in Karpenter
#
# Usage:
#   ./hack/scaffold-pool-feature.sh <FeatureName> <GoType> <MachineSection> <MachineField>
#
# Example:
#   ./hack/scaffold-pool-feature.sh GPUInstanceProfile "*string" Hardware GPUInstanceProfile
#   ./hack/scaffold-pool-feature.sh EnableSecureBoot "*bool" Security EnableSecureBoot
#
# This generates:
#   1. CRD field stub (for manual insertion into aksnodeclass.go)
#   2. configure*() helper for aksmachineinstancehelpers.go
#   3. Unit test stub for suite_aksmachineapi_features_test.go
#
# After running, you still need to:
#   - Insert the CRD field into AKSNodeClassSpec (both v1beta1 and v1alpha2)
#   - Wire configure*() call into buildAKSMachineTemplate()
#   - Run `make generate` to regenerate deepcopy and CRD YAML
#   - Bump AKSNodeClassHashVersion if the field affects drift
#   - Add instance type filtering if the feature has SKU constraints
#
# See designs/0009-pool-level-feature-guide.md for the full checklist.

set -euo pipefail

if [[ $# -lt 4 ]]; then
    echo "Usage: $0 <FeatureName> <GoType> <MachineSection> <MachineField>"
    echo ""
    echo "  FeatureName    PascalCase name (e.g., GPUInstanceProfile, EnableSecureBoot)"
    echo "  GoType         Go type for the CRD field (e.g., *string, *bool, *int32)"
    echo "  MachineSection Machine API section (Hardware, Security, OperatingSystem, Kubernetes, Network)"
    echo "  MachineField   Machine API field name within that section"
    echo ""
    echo "Example:"
    echo "  $0 GPUInstanceProfile '*string' Hardware GPUInstanceProfile"
    exit 1
fi

FEATURE_NAME="$1"
GO_TYPE="$2"
MACHINE_SECTION="$3"
MACHINE_FIELD="$4"

# Derive names
# For JSON, we lowercase the first letter. For acronym-prefixed names like GPUInstanceProfile,
# the user should pass the json name explicitly via a 5th arg, or fix manually.
LOWER_FIRST="$(echo "${FEATURE_NAME:0:1}" | tr '[:upper:]' '[:lower:]')${FEATURE_NAME:1}"
JSON_NAME="${5:-$LOWER_FIRST}"

echo "=== Pool-Level Feature Scaffold: $FEATURE_NAME ==="
echo ""

echo "--- 1. CRD Field (insert into AKSNodeClassSpec in both v1beta1 and v1alpha2) ---"
cat <<CRDEOF
	// ${LOWER_FIRST} configures the ${FEATURE_NAME} for nodes provisioned with this class.
	// +optional
	${FEATURE_NAME} ${GO_TYPE} \`json:"${JSON_NAME},omitempty"\`
CRDEOF
echo ""

echo "--- 2. configure*() helper (add to aksmachineinstancehelpers.go) ---"
cat <<HELPEREOF
func configure${FEATURE_NAME}(nodeClass *v1beta1.AKSNodeClass, aksMachine *armcontainerservice.Machine) {
	if nodeClass.Spec.${FEATURE_NAME} == nil {
		return
	}
	if aksMachine.Properties == nil {
		aksMachine.Properties = &armcontainerservice.MachineProperties{}
	}
	if aksMachine.Properties.${MACHINE_SECTION} == nil {
		aksMachine.Properties.${MACHINE_SECTION} = &armcontainerservice.Machine${MACHINE_SECTION}Profile{}
	}
	aksMachine.Properties.${MACHINE_SECTION}.${MACHINE_FIELD} = nodeClass.Spec.${FEATURE_NAME}
}
HELPEREOF
echo ""

echo "--- 3. Wire into buildAKSMachineTemplate() ---"
echo "Add this call in the buildAKSMachineTemplate function:"
echo "	configure${FEATURE_NAME}(nodeClass, aksMachine)"
echo ""

echo "--- 4. Unit test (add to suite_aksmachineapi_features_test.go) ---"
cat <<TESTEOF
	It("should set ${FEATURE_NAME} when specified on AKSNodeClass", func() {
		// nodeClass.Spec.${FEATURE_NAME} = lo.ToPtr(<value>)  // TODO: set appropriate test value
		ExpectApplied(ctx, env.Client, nodePool, nodeClass)
		pod := coretest.UnschedulablePod()
		ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, prov, pod)
		Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateRequests).To(HaveLen(1))
		machine := azureEnv.AKSMachinesAPI.AKSMachineCreateRequests[0].AKSMachine
		// Expect(machine.Properties.${MACHINE_SECTION}.${MACHINE_FIELD}).To(Equal(lo.ToPtr(<expected>)))  // TODO: set expected
	})

	It("should not set ${FEATURE_NAME} when not specified on AKSNodeClass", func() {
		ExpectApplied(ctx, env.Client, nodePool, nodeClass)
		pod := coretest.UnschedulablePod()
		ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, prov, pod)
		Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateRequests).To(HaveLen(1))
		machine := azureEnv.AKSMachinesAPI.AKSMachineCreateRequests[0].AKSMachine
		Expect(machine.Properties.${MACHINE_SECTION}.${MACHINE_FIELD}).To(BeNil())
	})
TESTEOF
echo ""

echo "--- 5. Remaining manual steps ---"
echo "  [ ] Insert CRD field into pkg/apis/v1beta1/aksnodeclass.go"
echo "  [ ] Insert CRD field into pkg/apis/v1alpha2/aksnodeclass.go"
echo "  [ ] Add configure*() to pkg/providers/instance/aksmachineinstancehelpers.go"
echo "  [ ] Wire configure*() into buildAKSMachineTemplate()"
echo "  [ ] Add unit tests to pkg/cloudprovider/suite_aksmachineapi_features_test.go"
echo "  [ ] Run: make generate"
echo "  [ ] Copy CRD: cp pkg/apis/crds/karpenter.azure.com_aksnodeclasses.yaml charts/karpenter-crd/templates/"
echo "  [ ] Consider: bump AKSNodeClassHashVersion if field affects drift"
echo "  [ ] Consider: instance type filtering if feature has SKU constraints"
echo "  [ ] Consider: bootstrapping client path (for VM-based provisioning)"
echo "  [ ] Consider: E2E integration test"
echo ""
echo "See designs/0009-pool-level-feature-guide.md for full details."

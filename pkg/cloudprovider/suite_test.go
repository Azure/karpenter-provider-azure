/*
Portions Copyright (c) Microsoft Corporation.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cloudprovider

// TODO v1beta1 extra refactor into suite_test.go / cloudprovider_test.go
import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/awslabs/operatorpkg/object"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	clock "k8s.io/utils/clock/testing"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/events"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
)

var ctx context.Context
var testOptions *options.Options
var stop context.CancelFunc
var env *coretest.Environment
var azureEnv *test.Environment
var azureEnvNonZonal *test.Environment
var coreProvisioner *provisioning.Provisioner
var coreProvisionerNonZonal *provisioning.Provisioner
var cloudProvider *CloudProvider
var cloudProviderNonZonal *CloudProvider
var cluster *state.Cluster
var clusterNonZonal *state.Cluster
var fakeClock *clock.FakeClock
var recorder events.Recorder
var statusController *status.Controller

var nodePool *karpv1.NodePool
var nodeClass *v1beta1.AKSNodeClass
var nodeClaim *karpv1.NodeClaim

var fakeZone1 = zones.MakeAKSLabelZoneFromARMZone(fake.Region, "1")
var defaultTestSKU = fake.MakeSKU("Standard_D2_v3")

func TestCloudProvider(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "cloudProvider/Azure")
}

func createSDKErrorBody(code, message string) io.ReadCloser {
	return io.NopCloser(bytes.NewReader([]byte(fmt.Sprintf(`{"error":{"code": "%s", "message": "%s"}}`, code, message))))
}

var _ = BeforeSuite(func() {
	env = coretest.NewEnvironment(coretest.WithCRDs(apis.CRDs...), coretest.WithCRDs(v1alpha1.CRDs...), coretest.WithFieldIndexers(coretest.NodeProviderIDFieldIndexer(ctx)))
	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx, stop = context.WithCancel(ctx)
	fakeClock = clock.NewFakeClock(time.Now())
	recorder = events.NewRecorder(&record.FakeRecorder{})
})

var _ = AfterSuite(func() {
	stop()
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = BeforeEach(func() {
	nodeClass = test.AKSNodeClass()
	nodePool = coretest.NodePool(karpv1.NodePool{
		Spec: karpv1.NodePoolSpec{
			Template: karpv1.NodeClaimTemplate{
				Spec: karpv1.NodeClaimTemplateSpec{
					NodeClassRef: &karpv1.NodeClassReference{
						Group: object.GVK(nodeClass).Group,
						Kind:  object.GVK(nodeClass).Kind,
						Name:  nodeClass.Name,
					},
				},
			},
		},
	})

	nodeClaim = coretest.NodeClaim(karpv1.NodeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{karpv1.NodePoolLabelKey: nodePool.Name},
		},
		Spec: karpv1.NodeClaimSpec{
			NodeClassRef: &karpv1.NodeClassReference{
				Group: object.GVK(nodeClass).Group,
				Kind:  object.GVK(nodeClass).Kind,
				Name:  nodeClass.Name,
			},
		},
	})
})

var _ = AfterEach(func() {
	ExpectCleanedUp(ctx, env.Client)
})

// Helper functions for NodeClaim validation
func validateNodeClaimCommon(nodeClaim *karpv1.NodeClaim, nodePool *karpv1.NodePool) {
	// Basic validation
	Expect(nodeClaim).ToNot(BeNil())
	Expect(nodeClaim.Status.Capacity).ToNot(BeEmpty())

	// Status fields
	Expect(nodeClaim.Status.ProviderID).ToNot(BeEmpty())
	Expect(nodeClaim.Status.ImageID).ToNot(BeEmpty())

	// Common labels validation
	Expect(nodeClaim.Labels).To(HaveKey(karpv1.CapacityTypeLabelKey))
	Expect(nodeClaim.Labels).To(HaveKey(karpv1.NodePoolLabelKey))
	if nodePool != nil {
		Expect(nodeClaim.Labels[karpv1.NodePoolLabelKey]).To(Equal(nodePool.Name))
	}
	Expect(nodeClaim.Labels).To(HaveKey(v1.LabelInstanceTypeStable))
	Expect(nodeClaim.Labels).To(HaveKey(v1.LabelArchStable))
	Expect(nodeClaim.Labels).To(HaveKey(v1beta1.LabelSKUName))
	Expect(nodeClaim.Labels).To(HaveKey(v1beta1.LabelSKUFamily))
	Expect(nodeClaim.Labels).To(HaveKey(v1beta1.LabelSKUCPU))
	Expect(nodeClaim.Labels).To(HaveKey(v1beta1.LabelSKUMemory))

	// Zone validation (conditional)
	zone := nodeClaim.Labels[v1.LabelTopologyZone]
	if zone != "" && zone != zones.Regional {
		Expect(zone).To(MatchRegexp(`^[a-z0-9-]+-[0-9]+$`))
	}

	// Capacity and Allocatable resources
	Expect(nodeClaim.Status.Capacity).To(HaveKey(v1.ResourceCPU))
	Expect(nodeClaim.Status.Capacity).To(HaveKey(v1.ResourceMemory))
	Expect(nodeClaim.Status.Allocatable).To(HaveKey(v1.ResourceCPU))
	Expect(nodeClaim.Status.Allocatable).To(HaveKey(v1.ResourceMemory))

	// Lifecycle validation
	Expect(nodeClaim.DeletionTimestamp).To(BeNil())
}

func validateVMNodeClaim(nodeClaim *karpv1.NodeClaim, nodePool *karpv1.NodePool) {
	// Common validations
	validateNodeClaimCommon(nodeClaim, nodePool)

	// VM-specific validation (should NOT have AKS machine annotation)
	Expect(nodeClaim.Annotations).ToNot(HaveKey(v1beta1.AnnotationAKSMachineResourceID))
}

func validateAKSMachineNodeClaim(nodeClaim *karpv1.NodeClaim, nodePool *karpv1.NodePool) {
	// Common validations
	validateNodeClaimCommon(nodeClaim, nodePool)

	// AKS-specific annotations
	Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationAKSMachineResourceID))
	Expect(nodeClaim.Annotations[v1beta1.AnnotationAKSMachineResourceID]).ToNot(BeEmpty())
}

type provisionModeKind string

const (
	provisionModeKindScriptless               provisionModeKind = "scriptless"
	provisionModeKindBootstrappingClient      provisionModeKind = "bootstrappingClient"
	provisionModeKindAKSMachineAPI            provisionModeKind = "aksMachineAPI"
	provisionModeKindAKSMachineAPIHeaderBatch provisionModeKind = "aksMachineAPIHeaderBatch"
)

type provisionModeTestCase struct {
	name                  string
	kind                  provisionModeKind
	validateNodeClaim     func(*karpv1.NodeClaim)
	resetCreateCalls      func()
	expectCreateCalls     func()
	expectCreatedResource func()
	resetListCalls        func()
	expectListCalls       func()
	resetGetCalls         func()
	expectGetCalls        func()
	resetDeleteCalls      func()
	expectDeleteCalls     func()
}

func (p provisionModeTestCase) isAKSMachineAPIHeaderBatchMode() bool {
	return p.kind == provisionModeKindAKSMachineAPIHeaderBatch
}

func (p provisionModeTestCase) isAKSMachineMode() bool {
	switch p.kind {
	case provisionModeKindAKSMachineAPI, provisionModeKindAKSMachineAPIHeaderBatch:
		return true
	case provisionModeKindScriptless, provisionModeKindBootstrappingClient:
		return false
	default:
		Fail(fmt.Sprintf("unknown provision mode kind %q for %q", p.kind, p.name))
		return false
	}
}

func aksscriptlessProvisionMode() provisionModeTestCase {
	return provisionModeTestCase{
		name: "AKSScriptless",
		kind: provisionModeKindScriptless,
		validateNodeClaim: func(nodeClaim *karpv1.NodeClaim) {
			validateVMNodeClaim(nodeClaim, nodePool)
		},
		resetCreateCalls: func() {
			azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
			azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
		},
		expectCreateCalls: func() {
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
		},
		expectCreatedResource: func() {
			createInput := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
			Expect(createInput.VM.Properties).ToNot(BeNil())
		},
		resetListCalls: func() {
			azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Reset()
			azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Reset()
		},
		expectListCalls: func() {
			if testOptions.ManageExistingAKSMachines {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Len()).To(Equal(1))
			} else {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Len()).To(Equal(0))
			}
			Expect(azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Len()).To(Equal(1))
		},
		resetGetCalls: func() {
			azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Reset()
			azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
		},
		expectGetCalls: func() {
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(0))
		},
		resetDeleteCalls: func() {
			azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Reset()
			azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Reset()
		},
		expectDeleteCalls: func() {
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(0))
		},
	}
}

func aksMachineAPIHeaderBatchProvisionMode() provisionModeTestCase {
	return provisionModeTestCase{
		name: "AKSMachineAPIHeaderBatch",
		kind: provisionModeKindAKSMachineAPIHeaderBatch,
		validateNodeClaim: func(nodeClaim *karpv1.NodeClaim) {
			validateAKSMachineNodeClaim(nodeClaim, nodePool)
		},
		resetCreateCalls: func() {
			azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
			azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
		},
		expectCreateCalls: func() {
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
		},
		expectCreatedResource: func() {
			createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
			Expect(createInput.AKSMachine.Properties).ToNot(BeNil())
		},
		resetListCalls: func() {
			azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Reset()
			azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Reset()
		},
		expectListCalls: func() {
			Expect(azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Len()).To(Equal(1))
		},
		resetGetCalls: func() {
			azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
			azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Reset()
		},
		expectGetCalls: func() {
			Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Len()).To(Equal(0))
		},
		resetDeleteCalls: func() {
			azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Reset()
			azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Reset()
		},
		expectDeleteCalls: func() {
			Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Len()).To(Equal(0))
		},
	}
}

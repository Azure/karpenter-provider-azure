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

package fleet_test

import (
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"
)

var env *azure.Environment
var nodeClass *v1beta1.AKSNodeClass
var nodePool *karpv1.NodePool

func TestFleet(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	AfterSuite(func() {
		env.Stop()
	})
	RunSpecs(t, "Fleet")
}

var _ = BeforeEach(func() {
	if env.ProvisionMode != consts.ProvisionModeFleet {
		Skip("fleet mode not enabled (set PROVISION_MODE=fleet)")
	}
	env.BeforeEach()
	nodeClass = env.DefaultAKSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
	// Workaround: the default AKSNodeClass image (Ubuntu 22.04 gen2) is Gen2-only,
	// but the default "D" SKU family includes Gen1-only SKUs (e.g. Standard_D2_v3).
	// Fleet's vmSizesProfile is built from the full AcceptableSKUs list without
	// filtering by hypervisor generation, so ARM rejects the request with 400
	// "cannot boot Hypervisor Generation '2'". LabelSKUHyperVGeneration is in
	// v1beta1.RestrictedLabels (set BY karpenter on offerings, not user-selectable),
	// so we restrict by LabelSKUVersion instead: for family "D", v4+ is Gen2-only.
	// TODO: remove once the fleet provider filters SKUs against the resolved image.
	nodePool = test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
		Key:      v1beta1.LabelSKUVersion,
		Operator: corev1.NodeSelectorOpIn,
		Values:   []string{"4", "5", "6"},
	})
})
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

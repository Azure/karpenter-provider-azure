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

package machines_test

import (
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

var env *azure.Environment
var nodeClass *v1beta1.AKSNodeClass
var nodePool *karpv1.NodePool

func TestMachines(t *testing.T) {
	// Check provision mode before running the suite to avoid Ginkgo counting skipped specs as failures.
	// Using t.Skip() instead of Ginkgo's Skip() because in Ginkgo if you skip all tests in the suite it
	// counts it as a failure
	provisionMode := os.Getenv("PROVISION_MODE")
	if provisionMode != consts.ProvisionModeAKSMachineAPI {
		t.Skipf("Skipping machines suite: provision mode %q is not %s", provisionMode, consts.ProvisionModeAKSMachineAPI)
	}

	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
		if env.InClusterController {
			env.ExpectRunInClusterControllerWithMachineMode()
		}
	})
	AfterSuite(func() {
		env.Stop()
	})
	RunSpecs(t, "Machines")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultAKSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

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

package byok_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/common"
)

var env *common.Environment

func TestBYOK(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "BYOK")
}

var _ = BeforeSuite(func() {
	env = common.NewEnvironment(GinkgoT())
})

var _ = AfterSuite(func() {
	env.Stop()
})

var _ = AfterEach(func() {
	env.Cleanup()
	env.AfterEach()
})

var _ = BeforeEach(func() {
	env.BeforeEach()
})
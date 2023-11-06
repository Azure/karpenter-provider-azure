// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package integration_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/karpenter/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter/test/pkg/environment/azure"
	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"
)

var env *azure.Environment
var nodeClass *v1alpha2.AKSNodeClass
var nodePool *corev1beta1.NodePool

func TestIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	AfterSuite(func() {
		env.Stop()
	})
	RunSpecs(t, "Integration")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultAKSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

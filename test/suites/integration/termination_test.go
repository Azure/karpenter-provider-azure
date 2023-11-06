// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package integration_test

import (
	. "github.com/onsi/ginkgo/v2"

	"github.com/aws/karpenter-core/pkg/test"
)

var _ = Describe("Termination", func() {
	It("should terminate the node and the instance on deletion", func() {
		pod := test.Pod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.ExpectCreatedNodeCount("==", 1)

		nodes := env.Monitor.CreatedNodes()

		// Pod is deleted so that we don't re-provision after node deletion
		// NOTE: We have to do this right now to deal with a race condition in provisioner ownership
		// This can be removed once this race is resolved with the Machine
		env.ExpectDeleted(pod)

		// Node is deleted and now should be not found
		env.ExpectDeleted(nodes[0])
		env.EventuallyExpectNotFound(nodes[0])
	})
})

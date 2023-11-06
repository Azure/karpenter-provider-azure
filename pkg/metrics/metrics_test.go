// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package metrics_test

import (
	"testing"

	"github.com/Azure/karpenter/pkg/metrics"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestAzure(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Metrics Suite")
}

var _ = Describe("Image Selection Error Metrics", func() {
	BeforeEach(func() {
		metrics.ImageSelectionErrorCount.Reset()
	})

	Describe("ImageSelectionErrorCount", func() {
		It("should have no errors initially", func() {
			Expect(testutil.CollectAndCount(metrics.ImageSelectionErrorCount)).To(Equal(0))
		})

		It("should increment the error count for a family", func() {
			metrics.ImageSelectionErrorCount.WithLabelValues("Ubuntu2204").Inc()
			Expect(testutil.CollectAndCount(metrics.ImageSelectionErrorCount)).To(Equal(1))
		})
	})
})

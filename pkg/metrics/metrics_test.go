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

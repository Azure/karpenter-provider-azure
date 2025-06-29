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

package metrics

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"sigs.k8s.io/karpenter/pkg/operator/injection"
)

func TestAzure(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Metrics Suite")
}

var _ = Describe("Image Selection Error Metrics", func() {
	BeforeEach(func() {
		ImageSelectionErrorCount.Reset()
	})

	Describe("ImageSelectionErrorCount", func() {
		It("should have no errors initially", func() {
			Expect(testutil.CollectAndCount(ImageSelectionErrorCount)).To(Equal(0))
		})

		It("should increment the error count for a family", func() {
			ImageSelectionErrorCount.WithLabelValues("Ubuntu2204").Inc()
			Expect(testutil.CollectAndCount(ImageSelectionErrorCount)).To(Equal(1))
		})
	})
})

var _ = Describe("Method Duration With Async Metrics", func() {
	BeforeEach(func() {
		MethodDurationWithAsync.Reset()
	})

	Describe("MethodDurationWithAsync", func() {
		It("should have no durations initially", func() {
			Expect(testutil.CollectAndCount(MethodDurationWithAsync)).To(Equal(0))
		})

		It("should record a duration", func() {
			MethodDurationWithAsync.With(GetLabelsMapForCloudProviderDurationWithAsync(injection.WithControllerName(context.Background(), "test.controller"), "Create", nil)).Observe(1.23)
			Expect(testutil.CollectAndCount(MethodDurationWithAsync)).To(Equal(1))
		})

		It("should recognize different label combinations", func() {
			ctx1 := injection.WithControllerName(context.Background(), "test.controller1")
			ctx2 := injection.WithControllerName(context.Background(), "test.controller2")

			labels1 := GetLabelsMapForCloudProviderDurationWithAsync(ctx1, "Create", nil)
			MethodDurationWithAsync.With(labels1).Observe(1.5)
			MethodDurationWithAsync.With(labels1).Observe(2.5)
			MethodDurationWithAsync.With(labels1).Observe(3.5)

			labels2 := GetLabelsMapForCloudProviderDurationWithAsync(ctx2, "Delete", nil)
			MethodDurationWithAsync.With(labels2).Observe(0.8)
			MethodDurationWithAsync.With(labels2).Observe(4.7)

			labels3 := GetLabelsMapForCloudProviderDurationWithAsync(ctx1, "Create", errors.New("TestError"))
			MethodDurationWithAsync.With(labels3).Observe(2.1)

			// Verify we have 3 different metric series (label combinations)
			Expect(testutil.CollectAndCount(MethodDurationWithAsync)).To(Equal(3))
		})

		It("should correctly set error labels", func() {
			ctx := injection.WithControllerName(context.Background(), "test.controller")

			// Test with no error
			labels1 := GetLabelsMapForCloudProviderDurationWithAsync(ctx, "Create", nil)
			Expect(labels1[metricLabelError]).To(Equal(cloudProviderMetricLabelErrorNone))

			// Test with error
			labels2 := GetLabelsMapForCloudProviderDurationWithAsync(ctx, "Create", errors.New("TestError"))
			Expect(labels2[metricLabelError]).To(Equal(cloudProviderMetricLabelErrorUnknown))
		})
	})
})

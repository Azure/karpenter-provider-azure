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

package instance

import (
	coremetrics "github.com/Azure/karpenter-provider-azure/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	instanceSubsystem = "instance"

	imageID   = "image"
	errorCode = "error"
)

var (
	// VMCreateStartMetric tracks when VM creation starts.
	//
	// STABILITY: ALPHA - This metric may change or be removed without notice.
	VMCreateStartMetric = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: coremetrics.Namespace,
			Subsystem: instanceSubsystem,
			Name:      "vm_create_start_total",
			Help:      "Total number of VM creation operations started.",
		},
		[]string{imageID},
	)

	// VMCreateSyncFailureMetric tracks synchronous VM creation failures.
	//
	// STABILITY: ALPHA - This metric may change or be removed without notice.
	VMCreateSyncFailureMetric = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: coremetrics.Namespace,
			Subsystem: instanceSubsystem,
			Name:      "vm_create_sync_failure_total",
			Help:      "Total number of synchronous VM creation failures.",
		},
		[]string{imageID},
	)

	// VMCreateAsyncFailureMetric tracks asynchronous VM creation failures.
	//
	// STABILITY: ALPHA - This metric may change or be removed without notice.
	VMCreateAsyncFailureMetric = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: coremetrics.Namespace,
			Subsystem: instanceSubsystem,
			Name:      "vm_create_async_failure_total",
			Help:      "Failed to create virtual machine during LRO",
		},
		[]string{imageID},
	)

	// VMCreateResponseErrorMetric tracks VM creation response errors.
	//
	// STABILITY: ALPHA - This metric may change or be removed without notice.
	VMCreateResponseErrorMetric = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: coremetrics.Namespace,
			Subsystem: instanceSubsystem,
			Name:      "vm_create_response_error_total",
			Help:      "Total number of VM creation response errors.",
		},
		[]string{errorCode},
	)
)

func init() {
	crmetrics.Registry.MustRegister(
		VMCreateStartMetric,
		VMCreateSyncFailureMetric,
		VMCreateAsyncFailureMetric,
		VMCreateResponseErrorMetric,
	)
}

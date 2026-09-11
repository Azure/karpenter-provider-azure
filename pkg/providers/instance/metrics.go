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
	"maps"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	metrics "github.com/Azure/karpenter-provider-azure/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	instanceSubsystem = "instance"
	phaseSyncFailure  = "sync"
	phaseAsyncFailure = "async"
)

// We don't need to add disk specification since they are statically defined and can be traced with provided labels.
var (
	// VMCreateStartMetric tracks when VM creation starts.
	//
	// STABILITY: ALPHA - This metric may change or be removed without notice.
	VMCreateStartMetric = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metrics.Namespace,
			Subsystem: instanceSubsystem,
			Name:      "vm_create_start_total",
			Help:      "Total number of VM creation operations started.",
		},
		[]string{metrics.ImageLabel, metrics.SizeLabel, metrics.ZoneLabel, metrics.CapacityTypeLabel, metrics.NodePoolLabel},
	)

	// VMCreateFailureMetric tracks VM creation failures, regardless of phase.
	//
	// STABILITY: ALPHA - This metric may change or be removed without notice.
	VMCreateFailureMetric = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metrics.Namespace,
			Subsystem: instanceSubsystem,
			Name:      "vm_create_failure_total",
			Help:      "Total number of VM creation failures.",
		},
		[]string{metrics.ImageLabel, metrics.SizeLabel, metrics.ZoneLabel, metrics.CapacityTypeLabel, metrics.NodePoolLabel, metrics.PhaseLabel, metrics.ErrorCodeLabel},
	)

	// AKSMachineCreateStartMetric tracks when AKS Machine creation starts.
	//
	// STABILITY: ALPHA - This metric may change or be removed without notice.
	AKSMachineCreateStartMetric = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metrics.Namespace,
			Subsystem: instanceSubsystem,
			Name:      "aks_machine_create_start_total",
			Help:      "Total number of AKS Machine creation operations started.",
		},
		[]string{metrics.ImageLabel, metrics.SizeLabel, metrics.ZoneLabel, metrics.CapacityTypeLabel, metrics.NodePoolLabel},
	)

	// AKSMachineCreateFailureMetric tracks AKS Machine creation failures, regardless of phase.
	//
	// STABILITY: ALPHA - This metric may change or be removed without notice.
	AKSMachineCreateFailureMetric = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metrics.Namespace,
			Subsystem: instanceSubsystem,
			Name:      "aks_machine_create_failure_total",
			Help:      "Total number of AKS Machine creation failures.",
		},
		[]string{metrics.ImageLabel, metrics.SizeLabel, metrics.ZoneLabel, metrics.CapacityTypeLabel, metrics.NodePoolLabel, metrics.PhaseLabel, metrics.ErrorCodeLabel},
	)
)

func init() {
	crmetrics.Registry.MustRegister(
		VMCreateStartMetric,
		VMCreateFailureMetric,
		AKSMachineCreateStartMetric,
		AKSMachineCreateFailureMetric,
	)
}

func recordAKSMachineCreateFailure(labels prometheus.Labels, phase, errorCode string) {
	failureLabels := maps.Clone(labels)
	failureLabels[metrics.PhaseLabel] = phase
	if errorCode == "" {
		errorCode = "UnknownError"
	}
	failureLabels[metrics.ErrorCodeLabel] = errorCode
	AKSMachineCreateFailureMetric.With(failureLabels).Inc()
}

func aksMachineProvisioningErrorCode(provisioningError *armcontainerservice.ErrorDetail) (string, bool) {
	if provisioningError == nil {
		return "", false
	}
	if len(provisioningError.Details) > 0 && provisioningError.Details[0] != nil && provisioningError.Details[0].Code != nil && *provisioningError.Details[0].Code != "" {
		return *provisioningError.Details[0].Code, true
	}
	if provisioningError.Code != nil && *provisioningError.Code != "" {
		return *provisioningError.Code, true
	}
	return "", false
}

func aksMachineProvisioningErrorCodeOrFallback(provisioningError *armcontainerservice.ErrorDetail, fallback string) string {
	if errorCode, ok := aksMachineProvisioningErrorCode(provisioningError); ok {
		return errorCode
	}
	return fallback
}

func aksMachineProvisioningErrorCodeForMetrics(provisioningError *armcontainerservice.ErrorDetail) string {
	return aksMachineProvisioningErrorCodeOrFallback(provisioningError, "UnknownError")
}

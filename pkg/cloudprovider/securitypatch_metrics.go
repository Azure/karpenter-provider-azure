// Portions Copyright (c) Microsoft Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cloudprovider

import (
	"github.com/Azure/karpenter-provider-azure/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var securityPatchFallbacks = prometheus.NewCounter(prometheus.CounterOpts{
	Namespace: metrics.Namespace,
	Name:      "securitypatch_standard_fallback_total",
	Help:      "Successful new-node create completions using a standard image on the SecurityPatch channel.",
})

func init() { crmetrics.Registry.MustRegister(securityPatchFallbacks) }

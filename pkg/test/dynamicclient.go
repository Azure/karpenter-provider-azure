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

package test

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// NewLocalDNSDynamicFakeClient returns a fake dynamic client preconfigured
// with the GVR -> List-Kind mapping for the Cilium and Calico CRDs that the
// LocalDNS status sub-reconciler may List during its cluster-gate checks.
// Without these registrations, the underlying fake client panics when it
// tries to resolve the list kind for an unregistered GVR.
func NewLocalDNSDynamicFakeClient() *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		{Group: "cilium.io", Version: "v2", Resource: "ciliumnetworkpolicies"}:             "CiliumNetworkPolicyList",
		{Group: "cilium.io", Version: "v2", Resource: "ciliumclusterwidenetworkpolicies"}:  "CiliumClusterwideNetworkPolicyList",
		{Group: "crd.projectcalico.org", Version: "v1", Resource: "networkpolicies"}:       "NetworkPolicyList",
		{Group: "crd.projectcalico.org", Version: "v1", Resource: "globalnetworkpolicies"}: "GlobalNetworkPolicyList",
	})
}

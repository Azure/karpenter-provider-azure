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
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"
)

func NodePool(options karpv1.NodePool) *karpv1.NodePool {
	nodePool := NodePoolWithShortName(options)
	nodePool.SetDefaults(context.Background())
	return nodePool
}

// NodePoolWithShortName leaves room for the suffix added to provisioned NodeClaim names.
func NodePoolWithShortName(overrides ...karpv1.NodePool) *karpv1.NodePool {
	return test.NodePool(append([]karpv1.NodePool{{
		ObjectMeta: metav1.ObjectMeta{Name: RandomName("nodepool")},
	}}, overrides...)...)
}

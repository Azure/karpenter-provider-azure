// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package test

import (
	"context"

	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"
	"github.com/aws/karpenter-core/pkg/test"
)

func NodePool(options corev1beta1.NodePool) *corev1beta1.NodePool {
	nodePool := test.NodePool(options)
	nodePool.SetDefaults(context.Background())
	return nodePool
}

// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package parameters

import (
	"github.com/Azure/karpenter/pkg/providers/imagefamily/bootstrap"
)

// StaticParameters define the static launch template parameters
type StaticParameters struct {
	ClusterName     string
	ClusterEndpoint string
	CABundle        *string

	TenantID                       string
	SubscriptionID                 string
	UserAssignedIdentityID         string
	Location                       string
	ResourceGroup                  string
	ClusterID                      string
	APIServerName                  string
	KubeletClientTLSBootstrapToken string
	NetworkPlugin                  string
	NetworkPolicy                  string
	KubernetesVersion              string

	Tags   map[string]string
	Labels map[string]string
}

// Parameters adds the dynamically generated launch template parameters
type Parameters struct {
	*StaticParameters
	UserData bootstrap.Bootstrapper
	ImageID  string
}

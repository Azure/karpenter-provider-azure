// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package test

import (
	"fmt"

	"github.com/imdario/mergo"
	"github.com/samber/lo"

	azsettings "github.com/Azure/karpenter/pkg/apis/settings"
)

type SettingOptions struct {
	ClusterName                    *string
	ClusterEndpoint                *string
	ClusterID                      *string
	KubeletClientTLSBootstrapToken *string
	SSHPublicKey                   *string
	NetworkPlugin                  *string
	NetworkPolicy                  *string
	VMMemoryOverheadPercent        *float64
	NodeIdentities                 []string
	Tags                           map[string]string
}

func Settings(overrides ...SettingOptions) *azsettings.Settings {
	options := SettingOptions{}
	for _, override := range overrides {
		if err := mergo.Merge(&options, override, mergo.WithOverride); err != nil {
			panic(fmt.Sprintf("Failed to merge settings: %s", err))
		}
	}
	return &azsettings.Settings{
		ClusterName:                    lo.FromPtrOr(options.ClusterName, "test-cluster"),
		ClusterEndpoint:                lo.FromPtrOr(options.ClusterEndpoint, "https://test-cluster"),
		ClusterID:                      lo.FromPtrOr(options.ClusterID, "00000000"),
		KubeletClientTLSBootstrapToken: lo.FromPtrOr(options.KubeletClientTLSBootstrapToken, "test-token"),
		SSHPublicKey:                   lo.FromPtrOr(options.SSHPublicKey, "test-ssh-public-key"),
		NetworkPlugin:                  lo.FromPtrOr(options.NetworkPlugin, "kubenet"),
		NetworkPolicy:                  lo.FromPtrOr(options.NetworkPolicy, ""),
		VMMemoryOverheadPercent:        lo.FromPtrOr(options.VMMemoryOverheadPercent, 0.075),
		NodeIdentities:                 options.NodeIdentities,
		Tags:                           options.Tags,
	}
}

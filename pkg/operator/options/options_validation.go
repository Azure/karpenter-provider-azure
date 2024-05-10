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

package options

import (
	"fmt"
	"net/url"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/go-playground/validator/v10"
	"go.uber.org/multierr"
)

func (o Options) Validate() error {
	validate := validator.New()
	return multierr.Combine(
		o.validateRequiredFields(),
		o.validateEndpoint(),
		o.validateVMMemoryOverheadPercent(),
		o.validateNetworkPluginMode(),
		o.validateVnetSubnetID(),
		validate.Struct(o),
	)
}

func (o Options) validateNetworkPluginMode() error {
	// TODO: Move overlay and none to shared constants in AgentBaker
	// NOTE: Network Plugin Mode should be normalized on the AKS API Level, so if we are passing values in from the ManagedCluster, NetworkPluginMode will already be normalized.
	if o.NetworkPluginMode != consts.PodNetworkTypeOverlay && o.NetworkPluginMode != "" && o.NetworkPluginMode != "none" {
		return fmt.Errorf("network-plugin-mode %v is invalid. network-plugin-mode must equal 'overlay', 'none' or ''", o.NetworkPluginMode)
	}
	return nil
}

func (o Options) validateVnetSubnetID() error {
	_, err := utils.GetVnetSubnetIDComponents(o.SubnetID)
	if err != nil {
		return fmt.Errorf("vnet-subnet-id is invalid: %w", err)
	}
	return nil
}

func (o Options) validateEndpoint() error {
	if o.ClusterEndpoint == "" {
		return nil
	}
	endpoint, err := url.Parse(o.ClusterEndpoint)
	// url.Parse() will accept a lot of input without error; make
	// sure it's a real URL
	if err != nil || !endpoint.IsAbs() || endpoint.Hostname() == "" {
		return fmt.Errorf("\"%s\" not a valid clusterEndpoint URL", o.ClusterEndpoint)
	}
	return nil
}

func (o Options) validateVMMemoryOverheadPercent() error {
	if o.VMMemoryOverheadPercent < 0 {
		return fmt.Errorf("vm-memory-overhead-percent cannot be negative")
	}
	return nil
}

func (o Options) validateRequiredFields() error {
	if o.ClusterEndpoint == "" {
		return fmt.Errorf("missing field, cluster-endpoint")
	}
	if o.ClusterName == "" {
		return fmt.Errorf("missing field, cluster-name")
	}
	if o.KubeletClientTLSBootstrapToken == "" {
		return fmt.Errorf("missing field, kubelet-bootstrap-token")
	}
	if o.SSHPublicKey == "" {
		return fmt.Errorf("missing field, ssh-public-key")
	}
	if o.SubnetID == "" {
		return fmt.Errorf("missing field, vnet-subnet-id")
	}
	return nil
}

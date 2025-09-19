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
	"regexp"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"go.uber.org/multierr"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

func (o *Options) Validate() error {
	validate := validator.New()
	return multierr.Combine(
		o.validateRequiredFields(),
		o.validateVNETGUID(),
		o.validateEndpoint(),
		o.validateNetworkingOptions(),
		o.validateVMMemoryOverheadPercent(),
		o.validateVnetSubnetID(),
		o.validateProvisionMode(),
		o.validateUseSIG(),
		o.validateAdminUsername(),
		o.validateAdditionalTags(),
		o.validateDiskEncryptionSetID(),
		validate.Struct(o),
	)
}

func (o *Options) validateVNETGUID() error {
	if o.VnetGUID != "" && uuid.Validate(o.VnetGUID) != nil {
		return fmt.Errorf("vnet-guid %s is malformed", o.VnetGUID)
	}
	return nil
}

func (o *Options) validateNetworkingOptions() error {
	if o.NetworkPlugin != consts.NetworkPluginAzure && o.NetworkPlugin != consts.NetworkPluginNone {
		return fmt.Errorf("network-plugin %v is invalid. network-plugin must equal 'azure' or 'none'", o.NetworkPlugin)
	}
	if o.NetworkPluginMode != consts.NetworkPluginModeOverlay && o.NetworkPluginMode != consts.NetworkPluginModeNone {
		return fmt.Errorf("network-plugin-mode %s is invalid. network-plugin-mode must equal 'overlay' or ''", o.NetworkPluginMode)
	}
	if o.NetworkDataplane != consts.NetworkDataplaneAzure && o.NetworkDataplane != consts.NetworkDataplaneCilium && o.NetworkDataplane != consts.NetworkDataplaneNone {
		return fmt.Errorf("network dataplane %s is not a valid network dataplane, valid dataplanes are ('azure', 'cilium')", o.NetworkDataplane)
	}

	if o.NetworkPlugin == consts.NetworkPluginNone && o.NetworkPluginMode != consts.NetworkPluginModeNone {
		return fmt.Errorf("network-plugin-mode '%s' is invalid when network-plugin is 'none'. network-plugin-mode must be empty", o.NetworkPluginMode)
	}
	return nil
}

func (o *Options) validateVnetSubnetID() error {
	_, err := utils.GetVnetSubnetIDComponents(o.SubnetID)
	if err != nil {
		return fmt.Errorf("vnet-subnet-id is invalid: %w", err)
	}
	return nil
}

func (o *Options) validateEndpoint() error {
	if o.ClusterEndpoint == "" {
		return nil
	}
	if !isValidURL(o.ClusterEndpoint) {
		return fmt.Errorf("\"%s\" not a valid clusterEndpoint URL", o.ClusterEndpoint)
	}
	return nil
}

func (o *Options) validateVMMemoryOverheadPercent() error {
	if o.VMMemoryOverheadPercent < 0 {
		return fmt.Errorf("vm-memory-overhead-percent cannot be negative")
	}
	return nil
}

func (o *Options) validateProvisionMode() error {
	if o.ProvisionMode != consts.ProvisionModeAKSScriptless && o.ProvisionMode != consts.ProvisionModeBootstrappingClient {
		return fmt.Errorf("provision-mode is invalid: %s", o.ProvisionMode)
	}
	if o.ProvisionMode == consts.ProvisionModeBootstrappingClient {
		if o.NodeBootstrappingServerURL == "" {
			return fmt.Errorf("nodebootstrapping-server-url is required when provision-mode is bootstrappingclient")
		}
	}
	return nil
}

func (o *Options) validateRequiredFields() error {
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
	if o.NodeResourceGroup == "" {
		return fmt.Errorf("missing field, node-resource-group")
	}
	return nil
}

func (o *Options) validateUseSIG() error {
	if o.UseSIG {
		if o.SIGAccessTokenServerURL == "" {
			return fmt.Errorf("sig-access-token-server-url is required when use-sig is true")
		}
		if o.SIGSubscriptionID == "" {
			return fmt.Errorf("sig-subscription-id is required when use-sig is true")
		}
		if !isValidURL(o.SIGAccessTokenServerURL) {
			return fmt.Errorf("sig-access-token-server-url is not a valid URL")
		}
	}
	return nil
}

func (o *Options) validateAdminUsername() error {
	if len(o.LinuxAdminUsername) > 32 {
		return fmt.Errorf("linux-admin-username cannot be longer than 32 characters")
	}

	// Must start with a letter and only contain letters, numbers, hyphens, and underscores
	match, err := regexp.MatchString("^[A-Za-z][-A-Za-z0-9_]*$", o.LinuxAdminUsername)
	if err != nil {
		return fmt.Errorf("error validating linux-admin-username: %w", err)
	}
	if !match {
		return fmt.Errorf("linux-admin-username must start with a letter and only contain letters, numbers, hyphens, and underscores")
	}

	return nil
}

// validateAdditionalTags checks that additional tags are valid according to Azure's tag rules.
// - Keys must be unique (case-insensitive)
// - Keys must not exceed 512 characters
// - Values must not exceed 256 characters
// - Keys must not contain invalid characters: <, >, %, &, \, ?, /
func (o *Options) validateAdditionalTags() error {
	seen := make(map[string]struct{}, len(o.AdditionalTags))
	for key, value := range o.AdditionalTags {
		if len(key) > 512 {
			return fmt.Errorf("additional-tags key %q exceeds maximum length of 512 characters", key)
		}
		if len(value) > 256 {
			return fmt.Errorf("additional-tags value for key %q exceeds maximum length of 256 characters", key)
		}
		if strings.ContainsAny(key, `<>%&\?/`) {
			return fmt.Errorf("additional-tags key %q contains invalid characters. <, >, %%, &, \\, ?, / are not allowed", key)
		}
		if _, exists := seen[strings.ToLower(key)]; exists {
			return fmt.Errorf("additional-tags key %q is not unique (case-insensitive). Duplicate key found", key)
		}
		seen[strings.ToLower(key)] = struct{}{}
	}

	return nil
}

func isValidURL(u string) bool {
	endpoint, err := url.Parse(u)
	// url.Parse() will accept a lot of input without error; make
	// sure it's a real URL
	return err == nil && endpoint.IsAbs() && endpoint.Hostname() != ""
}

func (o *Options) validateDiskEncryptionSetID() error {
	if o.DiskEncryptionSetID == "" {
		return nil
	}

	// Check if it starts with /subscriptions/ first, as this is more specific
	if !strings.HasPrefix(strings.ToLower(o.DiskEncryptionSetID), "/subscriptions/") {
		return fmt.Errorf("disk-encryption-set-id is invalid: must start with /subscriptions/, got %s", o.DiskEncryptionSetID)
	}

	parts := strings.Split(o.DiskEncryptionSetID, "/")
	if len(parts) != 9 {
		return fmt.Errorf("disk-encryption-set-id is invalid: expected format /subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Compute/diskEncryptionSets/{diskEncryptionSetName}, got %s", o.DiskEncryptionSetID)
	}

	if !strings.EqualFold(parts[3], "resourceGroups") {
		return fmt.Errorf("disk-encryption-set-id is invalid: expected 'resourceGroups' at position 4, got %s", parts[3])
	}

	if !strings.EqualFold(parts[5], "providers") || !strings.EqualFold(parts[6], "Microsoft.Compute") {
		return fmt.Errorf("disk-encryption-set-id is invalid: expected 'providers/Microsoft.Compute' at positions 6-7, got %s/%s", parts[5], parts[6])
	}

	if !strings.EqualFold(parts[7], "diskEncryptionSets") {
		return fmt.Errorf("disk-encryption-set-id is invalid: expected 'diskEncryptionSets' at position 8, got %s", parts[7])
	}

	if parts[2] == "" || parts[4] == "" || parts[8] == "" {
		return fmt.Errorf("disk-encryption-set-id is invalid: subscription ID, resource group name, and disk encryption set name must not be empty")
	}

	return nil
}

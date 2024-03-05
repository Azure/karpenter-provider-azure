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

/* All the helper functions should be hosted by another public repo later. (e.g. agentbaker)
This action of populating cse_cmd.sh should happen in the Go binary on VHD.
Therefore, Karpenter will not use these helper functions once the Go binary is ready. */

package bootstrap

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"text/template"

	nbcontractv1 "github.com/Azure/agentbaker/pkg/proto/nbcontract/v1"
	"github.com/samber/lo"
)

var (
	//go:embed kubenet-cni.json.gtpl
	kubenetTemplateContent []byte
	//go:embed sysctl.conf
	sysctlTemplateContent []byte
	//go:embed  containerdfornbcontract.toml.gtpl
	containerdConfigTemplateTextForNBContract string
	containerdConfigTemplateForNBContract     = template.Must(
		template.New("containerdconfigfornbcontract").Funcs(getFuncMapForContainerdConfigTemplate()).Parse(containerdConfigTemplateTextForNBContract),
	)
)

func getFuncMap() template.FuncMap {
	return template.FuncMap{
		"derefString":                      deref[string],
		"derefBool":                        deref[bool],
		"getStringFromNetworkModeType":     getStringFromNetworkModeType,
		"getStringFromNetworkPluginType":   getStringFromNetworkPluginType,
		"getStringFromNetworkPolicyType":   getStringFromNetworkPolicyType,
		"getStringFromLoadBalancerSkuType": getStringFromLoadBalancerSkuType,
		"getBoolFromFeatureState":          getBoolFromFeatureState,
		"getBoolStringFromFeatureState":    getBoolStringFromFeatureState,
		"getBoolStringFromFeatureStatePtr": getBoolStringFromFeatureStatePtr,
		"getStringifiedMap":                getStringifiedMap,
		"getKubenetTemplate":               getKubenetTemplate,
		"getSysctlContent":                 getSysctlContent,
		"getContainerdConfig":              getContainerdConfig,
		"getStringifiedStringArray":        getStringifiedStringArray,
		"getIsMIGNode":                     getIsMIGNode,
	}
}

func getFuncMapForContainerdConfigTemplate() template.FuncMap {
	return template.FuncMap{
		"derefBool":               deref[bool],
		"getBoolFromFeatureState": getBoolFromFeatureState,
	}
}

func getStringFromNetworkModeType(enum nbcontractv1.NetworkModeType) string {
	switch enum {
	case nbcontractv1.NetworkModeType_NETWORK_MODE_TRANSPARENT:
		return "transparent"
	case nbcontractv1.NetworkModeType_NETWORK_MODE_L2BRIDGE:
		return "l2bridge"
	default:
		return ""
	}
}

func getStringFromNetworkPluginType(enum nbcontractv1.NetworkPluginType) string {
	switch enum {
	case nbcontractv1.NetworkPluginType_NETWORK_PLUGIN_TYPE_AZURE:
		return "azure"
	case nbcontractv1.NetworkPluginType_NETWORK_PLUGIN_TYPE_KUBENET:
		return "kubenet"
	default:
		return ""
	}
}

func getStringFromNetworkPolicyType(enum nbcontractv1.NetworkPolicyType) string {
	switch enum {
	case nbcontractv1.NetworkPolicyType_NETWORK_POLICY_TYPE_AZURE:
		return "azure"
	case nbcontractv1.NetworkPolicyType_NETWORK_POLICY_TYPE_CALICO:
		return "calico"
	default:
		return ""
	}
}

func getStringFromLoadBalancerSkuType(enum nbcontractv1.LoadBalancerSku) string {
	switch enum {
	case nbcontractv1.LoadBalancerSku_LOAD_BALANCER_SKU_BASIC:
		return "Basic"
	case nbcontractv1.LoadBalancerSku_LOAD_BALANCER_SKU_STANDARD:
		return "Standard"
	default:
		return ""
	}
}

func getBoolFromFeatureState(state nbcontractv1.FeatureState) bool {
	return state == nbcontractv1.FeatureState_FEATURE_STATE_ENABLED
}

func getBoolStringFromFeatureState(state nbcontractv1.FeatureState) string {
	return strconv.FormatBool(state == nbcontractv1.FeatureState_FEATURE_STATE_ENABLED)
}

func getBoolStringFromFeatureStatePtr(state *nbcontractv1.FeatureState) string {
	if state == nil {
		return "false"
	}

	if *state == nbcontractv1.FeatureState_FEATURE_STATE_ENABLED {
		return "true"
	}

	return "false"
}

// deref is a helper function to dereference a pointer of any type to its value
func deref[T interface{}](p *T) T {
	return *p
}

func getStringifiedMap(m map[string]string, delimiter string) string {
	result := strings.Join(lo.MapToSlice(m, func(k, v string) string {
		return fmt.Sprintf("%s=%s", k, v)
	}), delimiter)
	return result
}

func getStringifiedStringArray(arr []string, delimiter string) string {
	if len(arr) == 0 {
		return ""
	}

	return strings.Join(arr, delimiter)
}

// getKubenetTemplate returns the base64 encoded Kubenet template.
func getKubenetTemplate() string {
	return base64.StdEncoding.EncodeToString(kubenetTemplateContent)
}

// getSysctlContent returns the base64 encoded sysctl content.
func getSysctlContent() string {
	return base64.StdEncoding.EncodeToString(sysctlTemplateContent)
}

func getContainerdConfig(nbcontract *nbcontractv1.Configuration) string {
	if nbcontract == nil {
		return ""
	}

	containerdConfig, err := containerdConfigFromNodeBootstrapContract(nbcontract)
	if err != nil {
		return fmt.Sprintf("error getting containerd config from node bootstrap variables: %v", err)
	}

	return base64.StdEncoding.EncodeToString([]byte(containerdConfig))
}

func containerdConfigFromNodeBootstrapContract(nbcontract *nbcontractv1.Configuration) (string, error) {
	if nbcontract == nil {
		return "", fmt.Errorf("node bootstrap contract is nil")
	}

	var buffer bytes.Buffer
	if err := containerdConfigTemplateForNBContract.Execute(&buffer, nbcontract); err != nil {
		return "", fmt.Errorf("error executing containerd config template for NBContract: %w", err)
	}

	return buffer.String(), nil
}

func getIsMIGNode(gpuInstanceProfile string) bool {
	return gpuInstanceProfile != ""
}

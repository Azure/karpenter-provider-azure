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

package customscriptsbootstrap

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/models"
	"k8s.io/apimachinery/pkg/api/resource"
)

func hydrateBootstrapTokenIfNeeded(customDataDehydratable string, cseDehydratable string, bootstrapToken string) (string, string, error) {
	cseHydrated := strings.ReplaceAll(cseDehydratable, "{{.TokenID}}.{{.TokenSecret}}", bootstrapToken)

	decodedCustomDataDehydratableInBytes, err := base64.StdEncoding.DecodeString(customDataDehydratable)
	if err != nil {
		return "", "", err
	}
	decodedCustomDataHydrated := strings.ReplaceAll(string(decodedCustomDataDehydratableInBytes), "{{.TokenID}}.{{.TokenSecret}}", bootstrapToken)
	decodedCustomDataHydrated, err = hydrateIgnitionFiles(decodedCustomDataHydrated, bootstrapToken)
	if err != nil {
		return "", "", err
	}
	customDataHydrated := base64.StdEncoding.EncodeToString([]byte(decodedCustomDataHydrated))

	return customDataHydrated, cseHydrated, nil
}

// hydrateIgnitionFiles rewrites the bootstrap token placeholder inside every
// embedded storage.files[].contents.source of an Ignition custom-data document,
// including gzip-compressed files. AgentBaker emits Ignition (not CSE) for
// AzureContainerLinux, and the bootstrap token placeholder can end up inside a
// gzip'd file rather than the outer document.
func hydrateIgnitionFiles(customData string, bootstrapToken string) (string, error) {
	var ignition map[string]any
	// Not Ignition JSON (e.g. Ubuntu cloud-init) — pass through unchanged.
	if err := json.Unmarshal([]byte(customData), &ignition); err != nil {
		return customData, nil //nolint:nilerr
	}
	if ignition["ignition"] == nil {
		return customData, nil
	}
	storage, _ := ignition["storage"].(map[string]any)
	files, _ := storage["files"].([]any)

	for i, rawFile := range files {
		if err := hydrateIgnitionFile(rawFile, bootstrapToken, i); err != nil {
			return "", err
		}
	}

	hydrated, err := json.Marshal(ignition)
	if err != nil {
		return "", fmt.Errorf("marshal hydrated Ignition: %w", err)
	}
	return string(hydrated), nil
}

func hydrateIgnitionFile(rawFile any, bootstrapToken string, index int) error {
	const prefix = "data:;base64,"
	file, _ := rawFile.(map[string]any)
	contents, _ := file["contents"].(map[string]any)
	source, _ := contents["source"].(string)
	compression, _ := contents["compression"].(string)
	if !strings.HasPrefix(source, prefix) {
		return nil
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(source, prefix))
	if err != nil {
		return fmt.Errorf("decode Ignition file %d: %w", index, err)
	}
	if compression == "gzip" {
		payload, err = gunzipBytes(payload)
		if err != nil {
			return fmt.Errorf("decompress Ignition file %d: %w", index, err)
		}
	}

	hydrated := []byte(strings.ReplaceAll(string(payload), "{{.TokenID}}.{{.TokenSecret}}", bootstrapToken))
	if compression == "gzip" {
		hydrated, err = gzipBytes(hydrated)
		if err != nil {
			return fmt.Errorf("compress Ignition file %d: %w", index, err)
		}
	}
	contents["source"] = prefix + base64.StdEncoding.EncodeToString(hydrated)
	return nil
}

func gunzipBytes(payload []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func gzipBytes(payload []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(payload); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func reverseVMMemoryOverhead(vmMemoryOverheadPercent float64, adjustedMemory float64) float64 {
	// This is not the best way to do it... But will be refactored later, given that retrieving the original memory properly might involves some restructure.
	// Due to the fact that it is abstracted behind the cloudprovider interface.
	return adjustedMemory / (1 - vmMemoryOverheadPercent)
}

func ConvertContainerLogMaxSizeToMB(containerLogMaxSize string) *int32 {
	q, err := resource.ParseQuantity(containerLogMaxSize)
	if err == nil {
		// This could be improved later
		return lo.ToPtr(int32(math.Round(q.AsApproximateFloat64() / 1024 / 1024)))
	}
	return nil
}

func ConvertPodMaxPids(podPidsLimit *int64) *int32 {
	if podPidsLimit != nil {
		podPidsLimitInt64 := *podPidsLimit
		if podPidsLimitInt64 > int64(math.MaxInt32) {
			// This could be improved later
			return lo.ToPtr(int32(math.MaxInt32))
		} else if podPidsLimitInt64 < 0 {
			// This as well
			return lo.ToPtr(int32(-1))
		} else {
			return lo.ToPtr(int32(podPidsLimitInt64)) // golint:ignore G115 already check overflow
		}
	}
	return nil
}

// convertLocalDNSToModel converts v1beta1.LocalDNS to models.LocalDNSProfile
func convertLocalDNSToModel(localDNS *v1beta1.LocalDNS) *models.LocalDNSProfile {
	if localDNS == nil {
		return nil
	}

	profile := &models.LocalDNSProfile{}

	if localDNS.Mode != "" {
		mode := string(localDNS.Mode)
		profile.Mode = &mode
	}

	// Convert VnetDNSOverrides
	if len(localDNS.VnetDNSOverrides) > 0 {
		profile.VnetDNSOverrides = make(models.LocalDNSOverrides)
		for _, override := range localDNS.VnetDNSOverrides {
			if convertedOverride := convertLocalDNSZoneOverrideToModel(&override); convertedOverride != nil {
				profile.VnetDNSOverrides[override.Zone] = *convertedOverride
			}
		}
	}

	// Convert KubeDNSOverrides
	if len(localDNS.KubeDNSOverrides) > 0 {
		profile.KubeDNSOverrides = make(models.LocalDNSOverrides)
		for _, override := range localDNS.KubeDNSOverrides {
			if convertedOverride := convertLocalDNSZoneOverrideToModel(&override); convertedOverride != nil {
				profile.KubeDNSOverrides[override.Zone] = *convertedOverride
			}
		}
	}

	return profile
}

// convertLocalDNSZoneOverrideToModel converts v1beta1.LocalDNSZoneOverride to models.LocalDNSOverride
func convertLocalDNSZoneOverrideToModel(override *v1beta1.LocalDNSZoneOverride) *models.LocalDNSOverride {
	if override == nil {
		return nil
	}

	modelOverride := &models.LocalDNSOverride{}

	if override.QueryLogging != "" {
		queryLogging := string(override.QueryLogging)
		modelOverride.QueryLogging = &queryLogging
	}

	if override.Protocol != "" {
		protocol := string(override.Protocol)
		modelOverride.Protocol = &protocol
	}

	if override.ForwardDestination != "" {
		forwardDest := string(override.ForwardDestination)
		modelOverride.ForwardDestination = &forwardDest
	}

	if override.ForwardPolicy != "" {
		forwardPolicy := string(override.ForwardPolicy)
		modelOverride.ForwardPolicy = &forwardPolicy
	}

	if override.MaxConcurrent != nil {
		modelOverride.MaxConcurrent = override.MaxConcurrent
	}

	if override.CacheDuration.Duration != nil {
		seconds := int32(override.CacheDuration.Seconds())
		modelOverride.CacheDurationInSeconds = &seconds
	}

	if override.ServeStaleDuration.Duration != nil {
		seconds := int32(override.ServeStaleDuration.Seconds())
		modelOverride.ServeStaleDurationInSeconds = &seconds
	}

	if override.ServeStale != "" {
		serveStale := string(override.ServeStale)
		modelOverride.ServeStale = &serveStale
	}

	return modelOverride
}

// convertLinuxOSConfigToModel converts v1beta1.LinuxOSConfiguration to models.CustomLinuxOSConfig
func convertLinuxOSConfigToModel(linuxOSConfig *v1beta1.LinuxOSConfiguration) *models.CustomLinuxOSConfig {
	if linuxOSConfig == nil {
		return nil
	}

	result := &models.CustomLinuxOSConfig{
		Sysctls: convertSysctlConfigToModel(linuxOSConfig.Sysctls),
	}
	if linuxOSConfig.SwapFileSize != nil && *linuxOSConfig.SwapFileSize != "" {
		result.SwapFileSizeMB = ConvertContainerLogMaxSizeToMB(*linuxOSConfig.SwapFileSize)
	}
	if linuxOSConfig.TransparentHugePageDefrag != nil {
		result.TransparentHugePageDefrag = lo.ToPtr(string(*linuxOSConfig.TransparentHugePageDefrag))
	}
	if linuxOSConfig.TransparentHugePageEnabled != nil {
		result.TransparentHugePageEnabled = lo.ToPtr(string(*linuxOSConfig.TransparentHugePageEnabled))
	}
	return result
}

// convertSysctlConfigToModel converts v1beta1.SysctlConfiguration to models.SysctlConfig
func convertSysctlConfigToModel(sysctls *v1beta1.SysctlConfiguration) *models.SysctlConfig {
	if sysctls == nil {
		return nil
	}

	return &models.SysctlConfig{
		FsAioMaxNr:                     sysctls.FsAioMaxNr,
		FsFileMax:                      sysctls.FsFileMax,
		FsInotifyMaxUserWatches:        sysctls.FsInotifyMaxUserWatches,
		FsNrOpen:                       sysctls.FsNrOpen,
		KernelThreadsMax:               sysctls.KernelThreadsMax,
		NetCoreNetdevMaxBacklog:        sysctls.NetCoreNetdevMaxBacklog,
		NetCoreOptmemMax:               sysctls.NetCoreOptmemMax,
		NetCoreRmemDefault:             sysctls.NetCoreRmemDefault,
		NetCoreRmemMax:                 sysctls.NetCoreRmemMax,
		NetCoreSomaxconn:               sysctls.NetCoreSomaxconn,
		NetCoreWmemDefault:             sysctls.NetCoreWmemDefault,
		NetCoreWmemMax:                 sysctls.NetCoreWmemMax,
		NetIPV4IPLocalPortRange:        sysctls.NetIPv4IPLocalPortRange,
		NetIPV4NeighDefaultGcThresh1:   sysctls.NetIPv4NeighDefaultGcThresh1,
		NetIPV4NeighDefaultGcThresh2:   sysctls.NetIPv4NeighDefaultGcThresh2,
		NetIPV4NeighDefaultGcThresh3:   sysctls.NetIPv4NeighDefaultGcThresh3,
		NetIPV4TCPFinTimeout:           sysctls.NetIPv4TCPFinTimeout,
		NetIPV4TCPKeepaliveProbes:      sysctls.NetIPv4TCPKeepaliveProbes,
		NetIPV4TCPKeepaliveTime:        sysctls.NetIPv4TCPKeepaliveTime,
		NetIPV4TCPMaxSynBacklog:        sysctls.NetIPv4TCPMaxSynBacklog,
		NetIPV4TCPMaxTwBuckets:         sysctls.NetIPv4TCPMaxTwBuckets,
		NetIPV4TCPTwReuse:              sysctls.NetIPv4TCPTwReuse,
		NetIPV4TcpkeepaliveIntvl:       sysctls.NetIPv4TCPKeepaliveIntvl,
		NetNetfilterNfConntrackBuckets: sysctls.NetNetfilterNfConntrackBuckets,
		NetNetfilterNfConntrackMax:     sysctls.NetNetfilterNfConntrackMax,
		VMMaxMapCount:                  sysctls.VMMaxMapCount,
		VMSwappiness:                   sysctls.VMSwappiness,
		VMVfsCachePressure:             sysctls.VMVfsCachePressure,
	}
}

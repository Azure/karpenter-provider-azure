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

package windows_test

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"
)

var env *azure.Environment

const (
	windowsPauseImage           = "mcr.microsoft.com/oss/kubernetes/pause:3.9"
	windows2025Image            = "mcr.microsoft.com/windows/servercore:ltsc2025"
	windows2022HostProcessImage = "mcr.microsoft.com/windows/servercore:ltsc2022"
	windows2025HostProcessImage = "mcr.microsoft.com/windows/servercore:ltsc2025"
	windowsNPDInventoryScript   = `
$ErrorActionPreference = 'Stop';
$ProgressPreference = 'SilentlyContinue'
$pattern = '(?i)(node[-_ ]?problem[-_ ]?detector|nodeproblemdetector|(^|[^a-z])npd([^a-z]|$))'
$services = @(Get-CimInstance Win32_Service)
$serviceMatches = @($services | Where-Object { $_.Name -match $pattern -or $_.DisplayName -match $pattern -or $_.PathName -match $pattern } | ForEach-Object { [ordered]@{ name = $_.Name; displayName = $_.DisplayName; state = $_.State; startMode = $_.StartMode } })
$windowsExporterServices = @($services | Where-Object { $_.Name -in @('aks-windows-exporter', 'windows-exporter') } | ForEach-Object { [ordered]@{ name = $_.Name; displayName = $_.DisplayName; state = $_.State; startMode = $_.StartMode } })
$processMatches = @(Get-CimInstance Win32_Process | Where-Object { $_.Name -match $pattern -or $_.ExecutablePath -match $pattern -or $_.CommandLine -match $pattern } | ForEach-Object { [ordered]@{ name = $_.Name; executableName = [IO.Path]::GetFileName($_.ExecutablePath) } })
$fileMatches = @()
foreach ($root in @('C:\k', 'C:\AzureData', 'C:\Packages\Plugins', 'C:\ProgramData\Microsoft\Azure')) {
    if (Test-Path -LiteralPath $root -ErrorAction SilentlyContinue) {
        $fileMatches += @(Get-ChildItem -LiteralPath $root -File -Recurse -Force -ErrorAction SilentlyContinue | Where-Object { $_.Name -match $pattern -or $_.FullName -match $pattern } | Select-Object -First 100 -ExpandProperty FullName)
    }
}
$pluginRoot = 'C:\Packages\Plugins'
$aksExtensionDirectories = @()
if (Test-Path -LiteralPath $pluginRoot -ErrorAction SilentlyContinue) {
    $aksExtensionDirectories = @(Get-ChildItem -LiteralPath $pluginRoot -Directory -Force -ErrorAction SilentlyContinue | Where-Object { $_.Name -like 'Microsoft.AKS.*' } | ForEach-Object { $_.FullName })
}
$healthz = [ordered]@{ reachable = $false; status = $null; bodyLength = $null; errorType = $null }
try {
    $response = Invoke-WebRequest -UseBasicParsing -Uri 'http://127.0.0.1:20256/healthz' -TimeoutSec 3
    $healthz.reachable = $true
    $healthz.status = [int]$response.StatusCode
    $healthz.bodyLength = [long]$response.RawContentLength
} catch {
    $healthz.errorType = $_.Exception.GetType().FullName
    if ($_.Exception.Response) {
        $healthz.reachable = $true
        $healthz.status = [int]$_.Exception.Response.StatusCode
    }
}
[ordered]@{ schemaVersion = 1; computerName = (Get-CimInstance Win32_ComputerSystem).Name; serviceMatches = $serviceMatches; windowsExporterServices = $windowsExporterServices; processMatches = $processMatches; fileMatches = $fileMatches; aksExtensionDirectories = $aksExtensionDirectories; healthz = $healthz } | ConvertTo-Json -Depth 6 -Compress
`
)

type windowsNPDInventory struct {
	SchemaVersion           int               `json:"schemaVersion"`
	ComputerName            string            `json:"computerName"`
	ServiceMatches          []json.RawMessage `json:"serviceMatches"`
	WindowsExporterServices []json.RawMessage `json:"windowsExporterServices"`
	ProcessMatches          []json.RawMessage `json:"processMatches"`
	FileMatches             []string          `json:"fileMatches"`
	AKSExtensionDirectories []string          `json:"aksExtensionDirectories"`
	Healthz                 json.RawMessage   `json:"healthz"`
}

type windowsNPDNodeCondition struct {
	Type   corev1.NodeConditionType `json:"type"`
	Status corev1.ConditionStatus   `json:"status"`
}

func windowsPowerShellEncodedCommand(script string) []string {
	units := utf16.Encode([]rune(script))
	buf := make([]byte, 0, len(units)*2) //nolint:mnd // UTF-16 code units are two bytes each.
	for _, unit := range units {
		buf = binary.LittleEndian.AppendUint16(buf, unit)
	}
	return []string{"powershell", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(buf)}
}

func TestWindows(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	AfterSuite(func() {
		env.Stop()
	})
	RunSpecs(t, "Windows")
}

var _ = BeforeEach(func() { env.BeforeEach() })
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("Windows", func() {
	BeforeEach(func() {
		env.SkipIfNotWindowsCapable()
	})

	It("should provision a Windows node and run a Windows pod", func() {
		imageFamily := os.Getenv("WINDOWS_IMAGE_FAMILY")
		if imageFamily == "" {
			imageFamily = v1beta1.Windows2022ImageFamily
		}

		var (
			containerImage string
			command        []string
			expectedOSSKU  string
		)
		switch imageFamily {
		case v1beta1.Windows2022ImageFamily:
			containerImage = windowsPauseImage
			expectedOSSKU = v1beta1.OSSKUWindows2022
		case v1beta1.Windows2025ImageFamily:
			containerImage = windows2025Image
			command = []string{"cmd", "/S", "/C", "ping -t 127.0.0.1"}
			expectedOSSKU = v1beta1.OSSKUWindows2025
		default:
			Fail(fmt.Sprintf("unsupported WINDOWS_IMAGE_FAMILY %q", imageFamily))
		}

		nodeClass := env.WindowsNodeClass(imageFamily)
		nodePool := env.WindowsNodePool(nodeClass)
		// Keep the test as simple and portable as possible: pin to the regional (zoneless)
		// offering so it provisions in subscriptions/regions without availability-zone support.
		// The SKU itself is intentionally left unconstrained: Karpenter pins the Windows node
		// image at create time and the image definition carries its Hyper-V generation, so
		// Windows provisions on any generation, including Gen2-only sizes.
		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			Key:      corev1.LabelTopologyZone,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{zones.Regional},
		})

		deployment := test.Deployment(test.DeploymentOptions{
			Replicas: 1,
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "windows-inflate"},
				},
				Image:   containerImage,
				Command: command,
				NodeSelector: map[string]string{
					corev1.LabelOSStable: string(corev1.Windows),
				},
				// Tolerate the OS taint Karpenter may surface during Windows node registration.
				Tolerations: []corev1.Toleration{{
					Key:      corev1.LabelOSStable,
					Operator: corev1.TolerationOpEqual,
					Value:    string(corev1.Windows),
					Effect:   corev1.TaintEffectNoSchedule,
				}},
				ResourceRequirements: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
				},
			},
		})

		env.ExpectCreated(nodeClass, nodePool, deployment)

		// Windows nodes take noticeably longer to provision and pull images than Linux.
		pods := env.EventuallyExpectHealthyDeploymentWithTimeout(25*time.Minute, deployment)
		env.ExpectCreatedNodeCount("==", 1)

		node := env.GetNode(pods[0].Spec.NodeName)
		Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelOSStable, string(corev1.Windows)))
		Expect(node.Labels).To(HaveKeyWithValue(v1beta1.AKSLabelOSSKU, expectedOSSKU))
		Expect(node.Labels).To(HaveKeyWithValue(karpv1.NodePoolLabelKey, nodePool.Name))
		if imageFamily == v1beta1.Windows2025ImageFamily {
			Expect(node.Labels).To(HaveKeyWithValue(v1beta1.AKSLabelFIPSEnabled, "true"))
		}

		collectWindowsNPDInventory(node, imageFamily)
	})
})

func collectWindowsNPDInventory(node *corev1.Node, imageFamily string) {
	GinkgoHelper()

	var image string
	switch imageFamily {
	case v1beta1.Windows2022ImageFamily:
		image = windows2022HostProcessImage
	case v1beta1.Windows2025ImageFamily:
		image = windows2025HostProcessImage
	default:
		Fail(fmt.Sprintf("unsupported Windows image family %q", imageFamily))
		return
	}

	probe := env.Pod(test.PodOptions{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "windows-npd-host-inventory-",
			Namespace:    "default",
		},
		Image:   image,
		Command: windowsPowerShellEncodedCommand(windowsNPDInventoryScript),
		NodeSelector: map[string]string{
			corev1.LabelOSStable: string(corev1.Windows),
		},
		Tolerations: []corev1.Toleration{{
			Key:      corev1.LabelOSStable,
			Operator: corev1.TolerationOpEqual,
			Value:    string(corev1.Windows),
			Effect:   corev1.TaintEffectNoSchedule,
		}},
		RestartPolicy: corev1.RestartPolicyNever,
	})
	probe.Spec.NodeName = node.Name
	probe.Spec.HostNetwork = true
	probe.Spec.SecurityContext = &corev1.PodSecurityContext{
		WindowsOptions: &corev1.WindowsSecurityContextOptions{
			HostProcess:   ptr.To(true),
			RunAsUserName: ptr.To(`NT AUTHORITY\SYSTEM`),
		},
	}
	probe.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{
		WindowsOptions: &corev1.WindowsSecurityContextOptions{
			HostProcess:   ptr.To(true),
			RunAsUserName: ptr.To(`NT AUTHORITY\SYSTEM`),
		},
	}

	podStatusContext := func(pod *corev1.Pod) string {
		var waitingReason, waitingMessage, terminatedReason, terminatedMessage, terminatedExitCode string
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name != probe.Spec.Containers[0].Name {
				continue
			}
			if status.State.Waiting != nil {
				waitingReason = status.State.Waiting.Reason
				waitingMessage = status.State.Waiting.Message
			}
			if status.State.Terminated != nil {
				terminatedReason = status.State.Terminated.Reason
				terminatedMessage = status.State.Terminated.Message
				terminatedExitCode = fmt.Sprint(status.State.Terminated.ExitCode)
			}
			break
		}
		return fmt.Sprintf("phase=%q reason=%q message=%q waitingReason=%q waitingMessage=%q terminatedReason=%q terminatedMessage=%q terminatedExitCode=%q",
			pod.Status.Phase, pod.Status.Reason, pod.Status.Message, waitingReason, waitingMessage, terminatedReason, terminatedMessage, terminatedExitCode)
	}

	env.ExpectCreated(probe)

	var currentPod corev1.Pod
	Eventually(func(g Gomega) {
		currentPod = corev1.Pod{}
		g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(probe), &currentPod)).To(Succeed())
		g.Expect(currentPod.Status.Phase).To(Or(Equal(corev1.PodSucceeded), Equal(corev1.PodFailed)), podStatusContext(&currentPod))
	}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

	var logs, logErrorType string
	if currentPod.Status.Phase == corev1.PodSucceeded {
		logs = strings.TrimSpace(env.EventuallyGetPodLogs(probe))
	} else {
		rawLogs, err := env.KubeClient.CoreV1().Pods(probe.Namespace).GetLogs(probe.Name, &corev1.PodLogOptions{
			Container: probe.Spec.Containers[0].Name,
		}).DoRaw(env.Context)
		if err == nil {
			logs = strings.TrimSpace(string(rawLogs))
		} else {
			logErrorType = fmt.Sprintf("%T", err)
		}
	}
	Expect(currentPod.Status.Phase).To(Equal(corev1.PodSucceeded),
		"Windows NPD inventory pod failed: %s logErrorType=%q logs=%q", podStatusContext(&currentPod), logErrorType, logs)

	var inventory windowsNPDInventory
	Expect(json.Unmarshal([]byte(logs), &inventory)).To(Succeed(), "Windows NPD inventory output must be valid JSON")
	Expect(inventory.SchemaVersion).To(Equal(1), "Windows NPD inventory schema version must be 1")
	Expect(strings.TrimSpace(inventory.ComputerName)).ToNot(BeEmpty(), "Windows NPD inventory computer name must not be empty")

	inventoryJSON, err := json.Marshal(inventory)
	Expect(err).ToNot(HaveOccurred())
	fmt.Fprintf(GinkgoWriter, "WINDOWS_NPD_HOST_INVENTORY=%s\n", inventoryJSON)

	refreshedNode := env.GetNode(node.Name)
	nodeConditions := make([]windowsNPDNodeCondition, 0)
	for _, condition := range refreshedNode.Status.Conditions {
		switch condition.Type {
		case corev1.NodeConditionType("KubeletProblem"),
			corev1.NodeConditionType("ContainerRuntimeProblem"),
			corev1.NodeConditionType("FilesystemCorruptionProblem"),
			corev1.NodeConditionType("FrequentKubeletRestart"),
			corev1.NodeConditionType("FrequentContainerdRestart"),
			corev1.NodeConditionType("VMEventScheduled"),
			corev1.NodeConditionType("FrequentUnregisterNetDevice"),
			corev1.NodeConditionType("FrequentDockerRestart"),
			corev1.NodeConditionType("KernelDeadlock"),
			corev1.NodeConditionType("ReadonlyFilesystem"):
			nodeConditions = append(nodeConditions, windowsNPDNodeCondition{
				Type:   condition.Type,
				Status: condition.Status,
			})
		}
	}

	nodeConditionsJSON, err := json.Marshal(nodeConditions)
	Expect(err).ToNot(HaveOccurred())
	fmt.Fprintf(GinkgoWriter, "WINDOWS_NPD_NODE_CONDITIONS=%s\n", nodeConditionsJSON)
}

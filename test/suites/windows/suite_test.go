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
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"
)

var env *azure.Environment

const windowsPauseImage = "mcr.microsoft.com/oss/kubernetes/pause:3.9"
const windows2025Image = "mcr.microsoft.com/windows/servercore:ltsc2025"

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

		Expect(pods[0].Spec.Containers).ToNot(BeEmpty())
		workloadPodName := pods[0].Name
		workloadPodNamespace := pods[0].Namespace
		workloadContainerName := pods[0].Spec.Containers[0].Name
		var workloadImageID, workloadNodeName string
		Eventually(func(g Gomega) {
			currentPod, err := env.KubeClient.CoreV1().Pods(workloadPodNamespace).Get(env.Context, workloadPodName, metav1.GetOptions{})
			g.Expect(err).ToNot(HaveOccurred())
			if err != nil {
				return
			}

			var workloadContainerStatus *corev1.ContainerStatus
			for i := range currentPod.Status.ContainerStatuses {
				if currentPod.Status.ContainerStatuses[i].Name == workloadContainerName {
					workloadContainerStatus = &currentPod.Status.ContainerStatuses[i]
					break
				}
			}
			g.Expect(workloadContainerStatus).ToNot(BeNil(), "expected workload pod %s/%s to report container %s", workloadPodNamespace, workloadPodName, workloadContainerName)
			if workloadContainerStatus == nil {
				return
			}
			g.Expect(workloadContainerStatus.Ready).To(BeTrue())
			g.Expect(workloadContainerStatus.State.Running).ToNot(BeNil())
			g.Expect(workloadContainerStatus.ImageID).ToNot(BeEmpty())
			g.Expect(currentPod.Spec.NodeName).ToNot(BeEmpty())
			workloadImageID = workloadContainerStatus.ImageID
			workloadNodeName = currentPod.Spec.NodeName
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
		By(fmt.Sprintf("Windows workload image ID: %s", workloadImageID))

		node := env.GetNode(workloadNodeName)
		By(fmt.Sprintf("Node %s labels: %+v", node.Name, node.Labels))
		Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelOSStable, string(corev1.Windows)))
		Expect(node.Labels).To(HaveKeyWithValue(v1beta1.AKSLabelOSSKU, expectedOSSKU))
		Expect(node.Labels).To(HaveKeyWithValue(karpv1.NodePoolLabelKey, nodePool.Name))
		if imageFamily == v1beta1.Windows2025ImageFamily {
			Expect(node.Labels).To(HaveKeyWithValue(v1beta1.AKSLabelFIPSEnabled, "true"))
		}

		By(fmt.Sprintf("Node %s conditions: %+v", node.Name, node.Status.Conditions))
		Expect(node.Status.Conditions).To(ContainElement(And(
			HaveField("Type", corev1.NodeReady),
			HaveField("Status", corev1.ConditionTrue),
		)), "expected Windows node %s to be Ready", node.Name)

		hostProcess := true
		automountServiceAccountToken := false
		runAsUserName := `NT AUTHORITY\SYSTEM`
		inventoryPod := env.Pod(test.PodOptions{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "windows-host-inventory-",
				Namespace:    "default",
			},
			Image:    "mcr.microsoft.com/oss/kubernetes/windows-host-process-containers-base-image@sha256:b4c9637e032f667c52d1eccfa31ad8c63f1b035e8639f3f48a510536bf34032b",
			NodeName: node.Name,
			Command: []string{
				`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`,
				"-NoLogo",
				"-NonInteractive",
				"-NoProfile",
				"-ExecutionPolicy",
				"Bypass",
				"-Command",
				`$ErrorActionPreference = 'Stop'
$npdPattern = '(?i)(node[-_. ]?problem[-_. ]?detector|(^|[^a-z0-9])npd([^a-z0-9]|$))'

function Get-ServiceState([object[]]$services, [string]$serviceName) {
    $service = $services | Where-Object { $_.Name -eq $serviceName } | Select-Object -First 1
    if ($null -eq $service) {
        return 'Absent'
    }
    return $service.Status.ToString()
}

# Allow one 30-minute reconciliation interval plus a five-minute buffer.
$exporterDeadline = (Get-Date).AddMinutes(35)
$consecutiveExporterRunningSamples = 0
$exporterServiceState = 'Absent'
while ($consecutiveExporterRunningSamples -lt 3) {
    $serviceSnapshot = @(Get-Service -ErrorAction Stop)
    $exporterServiceState = Get-ServiceState $serviceSnapshot 'aks-windows-exporter'
    if ($exporterServiceState -eq 'Running') {
        $consecutiveExporterRunningSamples++
    } else {
        $consecutiveExporterRunningSamples = 0
    }
    if ($consecutiveExporterRunningSamples -ge 3) {
        break
    }

    $remainingSeconds = ($exporterDeadline - (Get-Date)).TotalSeconds
    if ($remainingSeconds -le 0) {
        break
    }
    Start-Sleep -Seconds ([Math]::Min(30, [int]([Math]::Ceiling($remainingSeconds))))
}
if ($consecutiveExporterRunningSamples -lt 3) {
    throw ('aks-windows-exporter did not produce three consecutive Running samples; final state: {0}' -f $exporterServiceState)
}

$os = Get-CimInstance -ClassName Win32_OperatingSystem -ErrorAction Stop
if ($null -eq $os) {
    throw 'Win32_OperatingSystem returned no result'
}
$osVersion = '{0} {1} (Build {2})' -f $os.Caption, $os.Version, $os.BuildNumber
$processes = @(Get-CimInstance -ClassName Win32_Process -ErrorAction Stop)
$scheduledTasks = @(Get-ScheduledTask -ErrorAction Stop)

$kRoot = 'C:\k'
$pluginsRoot = 'C:\Packages\Plugins'
# Keep the broad C:\k walk depth-bounded, and only descend into AKSNode-related plugin roots.
$kEntries = @(Get-ChildItem -LiteralPath $kRoot -Force -Recurse -Depth 3 -ErrorAction Stop)
$pluginRootEntries = @(Get-ChildItem -LiteralPath $pluginsRoot -Force -ErrorAction Stop)
$aksNodeExtensionRoots = @(
    $pluginRootEntries |
        Where-Object { $_.PSIsContainer -and $_.Name -match '(?i)aksnode' }
)
$aksNodeExtensionPaths = @()
$aksNodeExtensionEntries = @()
foreach ($extensionRoot in $aksNodeExtensionRoots) {
    $aksNodeExtensionPaths += $extensionRoot.FullName
    $extensionEntries = @(
        Get-ChildItem -LiteralPath $extensionRoot.FullName -Force -Recurse -Depth 3 -ErrorAction Stop
    )
    $aksNodeExtensionEntries += $extensionEntries
    $aksNodeExtensionPaths += @(
        $extensionEntries |
            Where-Object { $_.PSIsContainer } |
            Select-Object -ExpandProperty FullName
    )
}
$aksNodeExtensionPaths = @($aksNodeExtensionPaths | Sort-Object -Unique)

$npdProcesses = @(
    $processes |
        Where-Object {
            $_.ProcessId -ne $PID -and (
                $_.Name -match $npdPattern -or
                $_.ExecutablePath -match $npdPattern -or
                $_.CommandLine -match $npdPattern
            )
        } |
        ForEach-Object { '{0}|{1}|{2}' -f $_.Name, $_.ProcessId, $_.ExecutablePath } |
        Sort-Object -Unique
)

$npdScheduledTasks = @(
    $scheduledTasks |
        Where-Object {
            $task = $_
            $matchingActions = @(
                $task.Actions |
                    Where-Object { $_.Execute -match $npdPattern -or $_.Arguments -match $npdPattern }
            )
            $task.TaskName -match $npdPattern -or
                $task.TaskPath -match $npdPattern -or
                $matchingActions.Count -gt 0
        } |
        ForEach-Object { '{0}{1}' -f $_.TaskPath, $_.TaskName } |
        Sort-Object -Unique
)

$pathEntries = @($kEntries) + @($pluginRootEntries) + @($aksNodeExtensionEntries)
$npdPaths = @(
    $pathEntries |
        Where-Object { $_.FullName -match $npdPattern } |
        Select-Object -ExpandProperty FullName |
        Sort-Object -Unique
)

# Re-query immediately before output so a crash during the inventory scans fails the contract.
$finalServices = @(Get-Service -ErrorAction Stop)
$kubeletServiceState = Get-ServiceState $finalServices 'kubelet'
$containerdServiceState = Get-ServiceState $finalServices 'containerd'
$exporterServiceState = Get-ServiceState $finalServices 'aks-windows-exporter'
if ($exporterServiceState -ne 'Running') {
    throw ('aks-windows-exporter stopped during inventory; final state: {0}' -f $exporterServiceState)
}
$npdServices = @(
    $finalServices |
        Where-Object { $_.Name -match $npdPattern -or $_.DisplayName -match $npdPattern } |
        ForEach-Object { '{0}|{1}|{2}' -f $_.Name, $_.DisplayName, $_.Status } |
        Sort-Object -Unique
)

Write-Output ('COMPUTER_NAME={0}' -f $env:COMPUTERNAME)
Write-Output ('IDENTITY={0}' -f [Security.Principal.WindowsIdentity]::GetCurrent().Name)
Write-Output ('OS_VERSION={0}' -f $osVersion)
Write-Output ('KUBELET_SERVICE_STATE={0}' -f $kubeletServiceState)
Write-Output ('CONTAINERD_SERVICE_STATE={0}' -f $containerdServiceState)
Write-Output ('AKS_WINDOWS_EXPORTER_SERVICE_STATE={0}' -f $exporterServiceState)
Write-Output ('AKSNODE_EXTENSION_PATHS={0}' -f ($aksNodeExtensionPaths -join ';'))
Write-Output ('NPD_SERVICES={0}' -f ($npdServices -join ';'))
Write-Output ('NPD_PROCESSES={0}' -f ($npdProcesses -join ';'))
Write-Output ('NPD_SCHEDULED_TASKS={0}' -f ($npdScheduledTasks -join ';'))
Write-Output ('NPD_PATHS={0}' -f ($npdPaths -join ';'))
Write-Output 'INVENTORY_COMPLETE=true'`,
			},
			RestartPolicy: corev1.RestartPolicyNever,
		})
		inventoryPod.Spec.HostNetwork = true
		inventoryPod.Spec.AutomountServiceAccountToken = &automountServiceAccountToken
		inventoryPod.Spec.SecurityContext = &corev1.PodSecurityContext{
			WindowsOptions: &corev1.WindowsSecurityContextOptions{
				HostProcess:   &hostProcess,
				RunAsUserName: &runAsUserName,
			},
		}

		env.ExpectCreated(inventoryPod)
		defer env.ExpectDeleted(inventoryPod)

		inventoryContainerName := inventoryPod.Spec.Containers[0].Name
		By("waiting for the Windows host inventory pod to succeed")
		Eventually(func(g Gomega) {
			currentPod, err := env.KubeClient.CoreV1().Pods(inventoryPod.Namespace).Get(env.Context, inventoryPod.Name, metav1.GetOptions{})
			g.Expect(err).ToNot(HaveOccurred())
			if err != nil {
				return
			}
			if currentPod.Status.Phase == corev1.PodFailed {
				containerLogs, logsErr := env.KubeClient.CoreV1().Pods(inventoryPod.Namespace).GetLogs(inventoryPod.Name, &corev1.PodLogOptions{
					Container: inventoryContainerName,
				}).DoRaw(env.Context)
				containerLogText := strings.TrimSpace(string(containerLogs))
				if logsErr != nil {
					containerLogText = fmt.Sprintf("<failed to fetch container logs: %v>", logsErr)
				} else if containerLogText == "" {
					containerLogText = "<empty>"
				}

				podEvents, eventsErr := env.KubeClient.CoreV1().Events(inventoryPod.Namespace).List(env.Context, metav1.ListOptions{
					FieldSelector: fmt.Sprintf("involvedObject.name=%s,involvedObject.uid=%s", currentPod.Name, currentPod.UID),
				})
				eventDetails := []string{}
				if eventsErr != nil {
					eventDetails = append(eventDetails, fmt.Sprintf("<failed to fetch pod events: %v>", eventsErr))
				} else {
					for _, event := range podEvents.Items {
						eventDetails = append(eventDetails, fmt.Sprintf("%s %s (count=%d): %s", event.Type, event.Reason, event.Count, event.Message))
					}
					if len(eventDetails) == 0 {
						eventDetails = append(eventDetails, "<none>")
					}
				}

				StopTrying(fmt.Sprintf(
					"Windows host inventory pod entered terminal Failed phase\nfull pod status: %+v\ncontainer logs:\n%s\npod events:\n%s",
					currentPod.Status,
					containerLogText,
					strings.Join(eventDetails, "\n"),
				)).Now()
				return
			}

			allContainerStatuses := make([]corev1.ContainerStatus, 0, len(currentPod.Status.InitContainerStatuses)+len(currentPod.Status.ContainerStatuses))
			allContainerStatuses = append(allContainerStatuses, currentPod.Status.InitContainerStatuses...)
			allContainerStatuses = append(allContainerStatuses, currentPod.Status.ContainerStatuses...)
			waitingReasons := []string{}
			for _, status := range allContainerStatuses {
				if status.State.Waiting != nil {
					waitingReasons = append(waitingReasons, fmt.Sprintf("%s: reason=%s message=%q", status.Name, status.State.Waiting.Reason, status.State.Waiting.Message))
				}
			}
			waitingReasonSummary := "<none reported>"
			if len(waitingReasons) > 0 {
				waitingReasonSummary = strings.Join(waitingReasons, "; ")
			}
			g.Expect(currentPod.Status.Phase).To(Equal(corev1.PodSucceeded),
				"Windows host inventory pod has not succeeded; full pod status: %+v; waiting reasons: %s",
				currentPod.Status,
				waitingReasonSummary,
			)
		}).WithTimeout(45 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())

		inventoryLogs := env.EventuallyGetPodLogs(inventoryPod)
		By(fmt.Sprintf("Windows host inventory from node %s:\n%s", node.Name, inventoryLogs))

		inventory := map[string]string{}
		for _, line := range strings.Split(inventoryLogs, "\n") {
			key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok {
				continue
			}
			inventory[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
		for _, key := range []string{
			"COMPUTER_NAME",
			"IDENTITY",
			"OS_VERSION",
			"KUBELET_SERVICE_STATE",
			"CONTAINERD_SERVICE_STATE",
			"AKS_WINDOWS_EXPORTER_SERVICE_STATE",
			"AKSNODE_EXTENSION_PATHS",
			"NPD_SERVICES",
			"NPD_PROCESSES",
			"NPD_SCHEDULED_TASKS",
			"NPD_PATHS",
			"INVENTORY_COMPLETE",
		} {
			Expect(inventory).To(HaveKey(key), "expected inventory output to contain %s", key)
		}

		Expect(strings.EqualFold(inventory["COMPUTER_NAME"], node.Name)).To(BeTrue(),
			"expected HostProcess computer name %q to identify node %q", inventory["COMPUTER_NAME"], node.Name)
		Expect(strings.EqualFold(inventory["IDENTITY"], runAsUserName)).To(BeTrue(),
			"expected HostProcess identity %q to be %q", inventory["IDENTITY"], runAsUserName)
		Expect(inventory["INVENTORY_COMPLETE"]).To(Equal("true"))
		Expect(inventory["OS_VERSION"]).ToNot(BeEmpty())
		Expect(inventory["KUBELET_SERVICE_STATE"]).To(Equal("Running"))
		Expect(inventory["CONTAINERD_SERVICE_STATE"]).To(Equal("Running"))
		Expect(inventory["AKS_WINDOWS_EXPORTER_SERVICE_STATE"]).To(Equal("Running"))
		for _, key := range []string{"NPD_SERVICES", "NPD_PROCESSES", "NPD_SCHEDULED_TASKS", "NPD_PATHS"} {
			Expect(inventory[key]).To(BeEmpty(), "expected no Windows NPD match in %s, found %q", key, inventory[key])
		}
	})
})

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

package common

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

const AgnHostTestImage = "registry.k8s.io/e2e-test-images/agnhost:2.66.1"

type dualStackAddresses struct {
	IPv4 string
	IPv6 string
}

func NetexecCommand(port int32) []string {
	return []string{"/agnhost", "netexec", fmt.Sprintf("--http-port=%d", port), "--udp-port=-1"}
}

func TCPReadinessProbe(port int32) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(port)},
		},
		PeriodSeconds:    2,
		FailureThreshold: 30,
	}
}

func DualStackServiceForDeployment(deployment *appsv1.Deployment, port int32) *corev1.Service {
	policy := corev1.IPFamilyPolicyRequireDualStack
	return &corev1.Service{
		ObjectMeta: test.NamespacedObjectMeta(),
		Spec: corev1.ServiceSpec{
			Selector: deployment.Spec.Selector.MatchLabels,
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Protocol:   corev1.ProtocolTCP,
				Port:       port,
				TargetPort: intstr.FromInt32(port),
			}},
			IPFamilies:     []corev1.IPFamily{corev1.IPv4Protocol, corev1.IPv6Protocol},
			IPFamilyPolicy: &policy,
		},
	}
}

func (env *Environment) EventuallyExpectDualStackServiceConnectivity(service *corev1.Service, backendPod *corev1.Pod, port int32, timeout time.Duration, additionalProbeOptions ...test.PodOptions) {
	GinkgoHelper()

	var serviceIPs, podIPs dualStackAddresses
	Eventually(func(g Gomega) {
		g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(service), service)).To(Succeed())
		g.Expect(service.Spec.IPFamilyPolicy).ToNot(BeNil())
		g.Expect(*service.Spec.IPFamilyPolicy).To(Equal(corev1.IPFamilyPolicyRequireDualStack))
		g.Expect(service.Spec.IPFamilies).To(Equal([]corev1.IPFamily{corev1.IPv4Protocol, corev1.IPv6Protocol}))
		serviceIPs = expectDualStackAddresses(g, service.Spec.ClusterIPs)

		g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(backendPod), backendPod)).To(Succeed())
		podAddresses := make([]string, 0, len(backendPod.Status.PodIPs))
		for _, podIP := range backendPod.Status.PodIPs {
			podAddresses = append(podAddresses, podIP.IP)
		}
		podIPs = expectDualStackAddresses(g, podAddresses)

		endpointSlices := &discoveryv1.EndpointSliceList{}
		g.Expect(env.Client.List(env.Context, endpointSlices,
			client.InNamespace(service.Namespace),
			client.MatchingLabels{discoveryv1.LabelServiceName: service.Name},
		)).To(Succeed())
		readyAddresses := map[discoveryv1.AddressType][]string{}
		for _, endpointSlice := range endpointSlices.Items {
			for _, endpoint := range endpointSlice.Endpoints {
				if endpoint.Conditions.Ready == nil || !*endpoint.Conditions.Ready {
					continue
				}
				readyAddresses[endpointSlice.AddressType] = append(readyAddresses[endpointSlice.AddressType], endpoint.Addresses...)
			}
		}
		g.Expect(readyAddresses[discoveryv1.AddressTypeIPv4]).To(ContainElement(podIPs.IPv4))
		g.Expect(readyAddresses[discoveryv1.AddressTypeIPv6]).To(ContainElement(podIPs.IPv6))
	}).WithTimeout(timeout).WithPolling(5 * time.Second).Should(Succeed())

	linuxProbeOptions := test.PodOptions{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"app": "dualstack-service-probe-linux"},
		},
		NodeSelector: map[string]string{
			corev1.LabelOSStable: string(corev1.Linux),
			v1beta1.AKSLabelMode: v1beta1.ModeSystem,
		},
		Tolerations: []corev1.Toleration{{
			Key:      "CriticalAddonsOnly",
			Operator: corev1.TolerationOpEqual,
			Value:    "true",
			Effect:   corev1.TaintEffectNoSchedule,
		}},
		ResourceRequirements: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			},
		},
	}
	env.eventuallyExpectDualStackConnectivityProbe(service, serviceIPs, podIPs, port, timeout, linuxProbeOptions, true)
	for _, probeOptions := range additionalProbeOptions {
		env.eventuallyExpectDualStackConnectivityProbe(service, serviceIPs, podIPs, port, timeout, probeOptions, false)
	}
}

func (env *Environment) eventuallyExpectDualStackConnectivityProbe(service *corev1.Service, serviceIPs, podIPs dualStackAddresses, port int32, timeout time.Duration, probeOptions test.PodOptions, validateDNSRecords bool) {
	GinkgoHelper()

	portString := strconv.Itoa(int(port))
	dnsName := fmt.Sprintf("%s.%s.svc.cluster.local", service.Name, service.Namespace)
	probeOptions.Image = AgnHostTestImage
	probeOptions.Command = []string{"/agnhost", "pause"}
	probeOptions.InitContainers = []corev1.Container{
		agnHostConnectContainer(net.JoinHostPort(podIPs.IPv4, portString)),
		agnHostConnectContainer(net.JoinHostPort(podIPs.IPv6, portString)),
		agnHostConnectContainer(net.JoinHostPort(serviceIPs.IPv4, portString)),
		agnHostConnectContainer(net.JoinHostPort(serviceIPs.IPv6, portString)),
		agnHostConnectContainer(net.JoinHostPort(dnsName, portString)),
	}
	if validateDNSRecords {
		probeOptions.InitContainers = append(probeOptions.InitContainers, corev1.Container{
			Image: AgnHostTestImage,
			Command: []string{"sh", "-c", fmt.Sprintf(
				"set -eu; test \"$(dig +short A %s)\" = '%s'; test \"$(dig +short AAAA %s)\" = '%s'",
				dnsName, serviceIPs.IPv4, dnsName, serviceIPs.IPv6,
			)},
		})
	}

	probe := test.Pod(probeOptions)
	env.ExpectCreated(probe)
	env.EventuallyExpectHealthyWithTimeout(timeout, probe)
}

func agnHostConnectContainer(target string) corev1.Container {
	return corev1.Container{
		Image:   AgnHostTestImage,
		Command: []string{"/agnhost", "connect", "--timeout=30s", target},
	}
}

func expectDualStackAddresses(g Gomega, addresses []string) dualStackAddresses {
	result := dualStackAddresses{}
	for _, address := range addresses {
		parsed, err := netip.ParseAddr(address)
		g.Expect(err).ToNot(HaveOccurred(), "expected %q to be an IP address", address)
		if parsed.Is4() {
			result.IPv4 = parsed.String()
		} else if parsed.Is6() {
			result.IPv6 = parsed.String()
		}
	}
	g.Expect(result.IPv4).ToNot(BeEmpty(), "expected an IPv4 address in %v", addresses)
	g.Expect(result.IPv6).ToNot(BeEmpty(), "expected an IPv6 address in %v", addresses)
	return result
}

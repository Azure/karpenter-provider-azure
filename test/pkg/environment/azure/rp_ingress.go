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

package azure

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const (
	rpURLEnv                = "RP_URL"
	rpHostEnv               = "RP_HOST"
	rpClientCertPathEnv     = "RP_CLIENT_CERT_PATH"
	rpServerCACertPathEnv   = "RP_SERVER_CA_CERT_PATH"
	machineAgentPoolNameEnv = "AKS_MACHINES_POOL_NAME"
	rpIngressHost           = "rp.e2e.ig.e2e-aks.azure.com"
)

type refererPolicy struct {
	value string
}

type rpIngressTransport struct {
	base http.RoundTripper
	host string
}

func (t *rpIngressTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := validateRPIngressRequest(request, t.host); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(request)
}

func validateRPIngressRequest(request *http.Request, rpHost string) error {
	if request == nil || request.URL == nil {
		return fmt.Errorf("RP ingress request URL is nil")
	}
	if request.URL.Scheme != "https" || request.URL.User != nil || request.URL.Opaque != "" || !rpIngressAuthorityMatches(request.URL.Host, rpHost) {
		return fmt.Errorf("refusing RP ingress request to non-exact origin %q", request.URL.String())
	}
	if request.Host != "" && !rpIngressAuthorityMatches(request.Host, rpHost) {
		return fmt.Errorf("refusing RP ingress request with Host %q", request.Host)
	}
	return nil
}

func rpIngressAuthorityMatches(authority, rpHost string) bool {
	if strings.EqualFold(authority, rpHost) {
		return true
	}
	host, port, err := net.SplitHostPort(authority)
	return err == nil && strings.EqualFold(host, rpHost) && port == "443"
}

func (p *refererPolicy) Do(request *policy.Request) (*http.Response, error) {
	request.Raw().Header.Set("Referer", p.value)
	return request.Next()
}

type rpIngressClientConfig struct {
	host            string
	physicalAddress string
	clientCert      tls.Certificate
	serverCAs       *x509.CertPool
}

type rpIngressEnvironmentValue struct {
	value string
	set   bool
}

func readRPIngressEnvironment() (map[string]rpIngressEnvironmentValue, bool, error) {
	names := []string{rpURLEnv, rpHostEnv, rpClientCertPathEnv, rpServerCACertPathEnv}
	values := make(map[string]rpIngressEnvironmentValue, len(names))
	configured := false
	for _, name := range names {
		value, set := os.LookupEnv(name)
		values[name] = rpIngressEnvironmentValue{value: strings.TrimSpace(value), set: set}
		configured = configured || set
	}
	if !configured {
		return values, false, nil
	}
	for _, name := range names {
		if value := values[name]; !value.set || value.value == "" {
			return nil, false, fmt.Errorf("%s, %s, %s, and %s must all be configured with non-empty values", rpURLEnv, rpHostEnv, rpClientCertPathEnv, rpServerCACertPathEnv)
		}
	}
	return values, true, nil
}

func parseRPIngressPhysicalAddress(value string) (string, error) {
	physicalURL, err := url.Parse(value)
	if err != nil || physicalURL.Scheme != "https" || physicalURL.Hostname() == "" ||
		physicalURL.User != nil || (physicalURL.EscapedPath() != "" && physicalURL.EscapedPath() != "/") ||
		physicalURL.RawQuery != "" || physicalURL.ForceQuery || physicalURL.Fragment != "" {
		return "", fmt.Errorf("invalid %s %q: expected an origin-only HTTPS URL", rpURLEnv, value)
	}
	if physicalURL.Port() == "" {
		return net.JoinHostPort(physicalURL.Hostname(), "443"), nil
	}
	return physicalURL.Host, nil
}

func loadRPIngressCertificates(clientPath, caPath string) (tls.Certificate, *x509.CertPool, error) {
	certData, err := os.ReadFile(clientPath)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("read %s: %w", rpClientCertPathEnv, err)
	}
	clientCert, err := tls.X509KeyPair(certData, certData)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("parse %s: %w", rpClientCertPathEnv, err)
	}
	caCertData, err := os.ReadFile(caPath)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("read %s: %w", rpServerCACertPathEnv, err)
	}
	serverCAs := x509.NewCertPool()
	if !serverCAs.AppendCertsFromPEM(caCertData) {
		return tls.Certificate{}, nil, fmt.Errorf("parse %s: no certificates found", rpServerCACertPathEnv)
	}
	return clientCert, serverCAs, nil
}

func loadRPIngressClientConfig() (*rpIngressClientConfig, bool, error) {
	values, configured, err := readRPIngressEnvironment()
	if err != nil || !configured {
		return nil, configured, err
	}
	host := values[rpHostEnv].value
	if host != rpIngressHost {
		return nil, false, fmt.Errorf("invalid %s %q: expected %q", rpHostEnv, host, rpIngressHost)
	}
	physicalAddress, err := parseRPIngressPhysicalAddress(values[rpURLEnv].value)
	if err != nil {
		return nil, false, err
	}
	clientCert, serverCAs, err := loadRPIngressCertificates(values[rpClientCertPathEnv].value, values[rpServerCACertPathEnv].value)
	if err != nil {
		return nil, false, err
	}
	return &rpIngressClientConfig{host: host, physicalAddress: physicalAddress, clientCert: clientCert, serverCAs: serverCAs}, true, nil
}

func cloudWithRPIngressEndpoint(baseCloud cloud.Configuration, host string) (cloud.Configuration, error) {
	rpCloud := baseCloud
	rpCloud.Services = make(map[cloud.ServiceName]cloud.ServiceConfiguration, len(baseCloud.Services))
	for name, service := range baseCloud.Services {
		rpCloud.Services[name] = service
	}
	resourceManager, ok := rpCloud.Services[cloud.ResourceManager]
	if !ok {
		return cloud.Configuration{}, fmt.Errorf("base cloud does not define the ResourceManager service")
	}
	resourceManager.Endpoint = "https://" + host
	rpCloud.Services[cloud.ResourceManager] = resourceManager
	return rpCloud, nil
}

func newRPIngressHTTPClient(config *rpIngressClientConfig) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	baseDialContext := transport.DialContext
	// RP_URL is the direct physical ingress destination. An ambient proxy would
	// receive the logical host instead and bypass the address mapping below.
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, splitErr := net.SplitHostPort(address)
		if splitErr != nil || !strings.EqualFold(host, config.host) || port != "443" {
			return nil, fmt.Errorf("refusing off-origin RP ingress dial to %q", address)
		}
		return baseDialContext(ctx, network, config.physicalAddress)
	}
	transport.TLSClientConfig = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{config.clientCert},
		RootCAs:      config.serverCAs,
		ServerName:   config.host,
	}
	return &http.Client{
		Transport: &rpIngressTransport{base: transport, host: config.host},
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			return validateRPIngressRequest(request, config.host)
		},
	}
}

func containerServiceClientOptions(baseCloud cloud.Configuration) (*arm.ClientOptions, error) {
	config, configured, err := loadRPIngressClientConfig()
	if err != nil {
		return nil, err
	}
	if !configured {
		return &arm.ClientOptions{ClientOptions: policy.ClientOptions{Cloud: baseCloud}}, nil
	}
	rpCloud, err := cloudWithRPIngressEndpoint(baseCloud, config.host)
	if err != nil {
		return nil, err
	}
	return &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Cloud:           rpCloud,
			Transport:       newRPIngressHTTPClient(config),
			PerCallPolicies: []policy.Policy{&refererPolicy{value: "https://" + config.host}},
		},
		DisableRPRegistration: true,
	}, nil
}

func machineAgentPoolName(inClusterController bool) string {
	if configured := strings.TrimSpace(os.Getenv(machineAgentPoolNameEnv)); configured != "" {
		return configured
	}
	if inClusterController {
		return "testmpool"
	}
	return "aksmanagedap"
}

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

func containerServiceClientOptions(baseCloud cloud.Configuration) (*arm.ClientOptions, error) {
	rpURLValue, rpURLSet := os.LookupEnv(rpURLEnv)
	rpHostValue, rpHostSet := os.LookupEnv(rpHostEnv)
	certPathValue, certPathSet := os.LookupEnv(rpClientCertPathEnv)
	caCertPathValue, caCertPathSet := os.LookupEnv(rpServerCACertPathEnv)
	if !rpURLSet && !rpHostSet && !certPathSet && !caCertPathSet {
		return &arm.ClientOptions{ClientOptions: policy.ClientOptions{Cloud: baseCloud}}, nil
	}
	rpURL := strings.TrimSpace(rpURLValue)
	rpHost := strings.TrimSpace(rpHostValue)
	certPath := strings.TrimSpace(certPathValue)
	caCertPath := strings.TrimSpace(caCertPathValue)
	if !rpURLSet || !rpHostSet || !certPathSet || !caCertPathSet || rpURL == "" || rpHost == "" || certPath == "" || caCertPath == "" {
		return nil, fmt.Errorf("%s, %s, %s, and %s must all be configured with non-empty values", rpURLEnv, rpHostEnv, rpClientCertPathEnv, rpServerCACertPathEnv)
	}

	physicalURL, err := url.Parse(rpURL)
	if err != nil || physicalURL.Scheme != "https" || physicalURL.Hostname() == "" ||
		physicalURL.User != nil || (physicalURL.EscapedPath() != "" && physicalURL.EscapedPath() != "/") ||
		physicalURL.RawQuery != "" || physicalURL.ForceQuery || physicalURL.Fragment != "" {
		return nil, fmt.Errorf("invalid %s %q: expected an origin-only HTTPS URL", rpURLEnv, rpURL)
	}
	physicalAddress := physicalURL.Host
	if physicalURL.Port() == "" {
		physicalAddress = net.JoinHostPort(physicalURL.Hostname(), "443")
	}
	if rpHost != rpIngressHost {
		return nil, fmt.Errorf("invalid %s %q: expected %q", rpHostEnv, rpHost, rpIngressHost)
	}

	certData, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rpClientCertPathEnv, err)
	}
	clientCert, err := tls.X509KeyPair(certData, certData)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rpClientCertPathEnv, err)
	}
	caCertData, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rpServerCACertPathEnv, err)
	}
	serverCAs := x509.NewCertPool()
	if !serverCAs.AppendCertsFromPEM(caCertData) {
		return nil, fmt.Errorf("parse %s: no certificates found", rpServerCACertPathEnv)
	}

	rpCloud := baseCloud
	rpCloud.Services = make(map[cloud.ServiceName]cloud.ServiceConfiguration, len(baseCloud.Services))
	for name, service := range baseCloud.Services {
		rpCloud.Services[name] = service
	}
	resourceManager, ok := rpCloud.Services[cloud.ResourceManager]
	if !ok {
		return nil, fmt.Errorf("base cloud does not define the ResourceManager service")
	}
	resourceManager.Endpoint = "https://" + rpHost
	rpCloud.Services[cloud.ResourceManager] = resourceManager

	transport := http.DefaultTransport.(*http.Transport).Clone()
	baseDialContext := transport.DialContext
	// RP_URL is the direct physical ingress destination. An ambient proxy would
	// receive the logical host instead and bypass the address mapping below.
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, splitErr := net.SplitHostPort(address)
		if splitErr != nil || !strings.EqualFold(host, rpHost) || port != "443" {
			return nil, fmt.Errorf("refusing off-origin RP ingress dial to %q", address)
		}
		return baseDialContext(ctx, network, physicalAddress)
	}
	transport.TLSClientConfig = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      serverCAs,
		ServerName:   rpHost,
	}

	transportClient := &http.Client{
		Transport: &rpIngressTransport{base: transport, host: rpHost},
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			return validateRPIngressRequest(request, rpHost)
		},
	}
	return &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Cloud:           rpCloud,
			Transport:       transportClient,
			PerCallPolicies: []policy.Policy{&refererPolicy{value: "https://" + rpHost}},
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

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
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	containerservice "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
)

type recordingCredential struct {
	requests []policy.TokenRequestOptions
}

func (c *recordingCredential) GetToken(_ context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.requests = append(c.requests, options)
	return azcore.AccessToken{Token: "token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestValidateRPIngressRequest(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		host    string
		wantErr bool
	}{
		{name: "exact HTTPS origin", url: "https://" + rpIngressHost + "/resource"},
		{name: "explicit HTTPS port", url: "https://" + rpIngressHost + ":443/resource"},
		{name: "plaintext downgrade", url: "http://" + rpIngressHost + "/resource", wantErr: true},
		{name: "alternate logical port", url: "https://" + rpIngressHost + ":444/resource", wantErr: true},
		{name: "off-origin URL", url: "https://management.azure.com/resource", wantErr: true},
		{name: "overridden Host header", url: "https://" + rpIngressHost + "/resource", host: "management.azure.com", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, tt.url, nil)
			if err != nil {
				t.Fatalf("create request: %v", err)
			}
			request.Host = tt.host
			err = validateRPIngressRequest(request, rpIngressHost)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validation error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestContainerServiceClientOptions(t *testing.T) {
	const rpHost = "rp.e2e.ig.e2e-aks.azure.com"

	t.Run("uses public ARM when ingress contract is absent", func(t *testing.T) {
		clearRPIngressEnvironment(t)
		options, err := containerServiceClientOptions(cloud.AzurePublic)
		if err != nil {
			t.Fatalf("build client options: %v", err)
		}
		got := options.Cloud.Services[cloud.ResourceManager].Endpoint
		want := cloud.AzurePublic.Services[cloud.ResourceManager].Endpoint
		if got != want {
			t.Fatalf("resource manager endpoint = %q, want %q", got, want)
		}
	})

	t.Run("rejects partial ingress contract", func(t *testing.T) {
		clearRPIngressEnvironment(t)
		t.Setenv("RP_URL", "https://127.0.0.1:443")
		if _, err := containerServiceClientOptions(cloud.AzurePublic); err == nil {
			t.Fatal("expected partial ingress contract to fail")
		}
	})

	t.Run("rejects present but empty ingress contract", func(t *testing.T) {
		clearRPIngressEnvironment(t)
		t.Setenv("RP_SERVER_CA_CERT_PATH", "")
		if _, err := containerServiceClientOptions(cloud.AzurePublic); err == nil {
			t.Fatal("expected present but empty ingress contract to fail")
		}
	})

	t.Run("rejects whitespace-only ingress contract", func(t *testing.T) {
		clearRPIngressEnvironment(t)
		t.Setenv("RP_URL", " ")
		t.Setenv("RP_HOST", "\t")
		t.Setenv("RP_CLIENT_CERT_PATH", "\n")
		t.Setenv("RP_SERVER_CA_CERT_PATH", "\r")
		if _, err := containerServiceClientOptions(cloud.AzurePublic); err == nil {
			t.Fatal("expected whitespace-only ingress contract to fail")
		}
	})

	t.Run("rejects non-origin physical URL", func(t *testing.T) {
		clearRPIngressEnvironment(t)
		t.Setenv("RP_URL", "https://127.0.0.1:443/build?run=1")
		t.Setenv("RP_HOST", rpHost)
		t.Setenv("RP_CLIENT_CERT_PATH", "unused")
		t.Setenv("RP_SERVER_CA_CERT_PATH", "unused")
		if _, err := containerServiceClientOptions(cloud.AzurePublic); err == nil || !strings.Contains(err.Error(), "invalid RP_URL") {
			t.Fatalf("physical URL error = %v, want invalid RP_URL", err)
		}
	})

	t.Run("rejects unexpected logical host", func(t *testing.T) {
		clearRPIngressEnvironment(t)
		t.Setenv("RP_URL", "https://127.0.0.1:443")
		t.Setenv("RP_HOST", "management.azure.com")
		t.Setenv("RP_CLIENT_CERT_PATH", "unused")
		t.Setenv("RP_SERVER_CA_CERT_PATH", "unused")
		if _, err := containerServiceClientOptions(cloud.AzurePublic); err == nil || !strings.Contains(err.Error(), "invalid RP_HOST") {
			t.Fatalf("logical host error = %v, want invalid RP_HOST", err)
		}
	})

	t.Run("rejects unreadable client certificate", func(t *testing.T) {
		clearRPIngressEnvironment(t)
		t.Setenv("RP_URL", "https://127.0.0.1:443")
		t.Setenv("RP_HOST", rpHost)
		t.Setenv("RP_CLIENT_CERT_PATH", filepath.Join(t.TempDir(), "missing.pem"))
		t.Setenv("RP_SERVER_CA_CERT_PATH", "unused")
		if _, err := containerServiceClientOptions(cloud.AzurePublic); err == nil {
			t.Fatal("expected unreadable certificate to fail")
		}
	})

	t.Run("routes logical ContainerService requests through mTLS ingress", func(t *testing.T) {
		clearRPIngressEnvironment(t)
		var requests atomic.Int32
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestNumber := requests.Add(1)
			if r.Host != rpHost {
				t.Errorf("host = %q, want %q", r.Host, rpHost)
			}
			if got, want := r.Header.Get("Referer"), "https://"+rpHost; got != want {
				t.Errorf("referer = %q, want %q", got, want)
			}
			if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
				t.Error("request did not present a client certificate")
			}
			w.Header().Set("Content-Type", "application/json")
			if requestNumber == 1 {
				nextLink := fmt.Sprintf("https://%s%s&$skiptoken=next", rpHost, r.URL.RequestURI())
				_, _ = fmt.Fprintf(w, `{"value":[{"id":"cluster","name":"cluster","type":"Microsoft.ContainerService/managedClusters","location":"eastus2","properties":{}}],"nextLink":%q}`, nextLink)
				return
			}
			if got := r.URL.Query().Get("$skiptoken"); got != "next" {
				t.Errorf("continuation token = %q, want next", got)
			}
			_, _ = w.Write([]byte(`{"value":[]}`))
		}))
		serverTLS, serverCAPEM := serverTLSConfig(t, rpHost)
		server.TLS = serverTLS
		server.StartTLS()
		defer server.Close()

		certPath := filepath.Join(t.TempDir(), "client.pem")
		if err := os.WriteFile(certPath, clientCertificatePEM(t), 0o600); err != nil {
			t.Fatalf("write client certificate: %v", err)
		}
		caPath := filepath.Join(t.TempDir(), "server-ca.pem")
		if err := os.WriteFile(caPath, serverCAPEM, 0o600); err != nil {
			t.Fatalf("write server CA certificate: %v", err)
		}
		t.Setenv("RP_URL", server.URL)
		t.Setenv("RP_HOST", rpHost)
		t.Setenv("RP_CLIENT_CERT_PATH", certPath)
		t.Setenv("RP_SERVER_CA_CERT_PATH", caPath)

		baseEndpoint := cloud.AzurePublic.Services[cloud.ResourceManager].Endpoint
		options, err := containerServiceClientOptions(cloud.AzurePublic)
		if err != nil {
			t.Fatalf("build client options: %v", err)
		}
		if got := cloud.AzurePublic.Services[cloud.ResourceManager].Endpoint; got != baseEndpoint {
			t.Fatalf("base cloud endpoint mutated to %q, want %q", got, baseEndpoint)
		}
		transportClient, ok := options.Transport.(*http.Client)
		if !ok {
			t.Fatalf("transport client type = %T, want *http.Client", options.Transport)
		}
		originTransport, ok := transportClient.Transport.(*rpIngressTransport)
		if !ok {
			t.Fatalf("transport type = %T, want *rpIngressTransport", transportClient.Transport)
		}
		transport, ok := originTransport.base.(*http.Transport)
		if !ok {
			t.Fatalf("base transport type = %T, want *http.Transport", originTransport.base)
		}
		if transport.Proxy != nil {
			t.Fatal("RP ingress transport inherited an ambient proxy")
		}
		if _, err := transport.DialContext(context.Background(), "tcp", "management.azure.com:443"); err == nil || !strings.Contains(err.Error(), "refusing off-origin RP ingress dial") {
			t.Fatalf("off-origin dial error = %v, want fail-closed error", err)
		}
		connection, err := transport.DialContext(context.Background(), "tcp", rpHost+":444")
		if connection != nil {
			_ = connection.Close()
		}
		if err == nil || !strings.Contains(err.Error(), "refusing off-origin RP ingress dial") {
			t.Fatalf("alternate-port dial error = %v, want fail-closed error", err)
		}
		redirectRequest, err := http.NewRequest(http.MethodGet, "http://"+rpHost+"/redirect", nil)
		if err != nil {
			t.Fatalf("create redirect request: %v", err)
		}
		if transportClient.CheckRedirect == nil || transportClient.CheckRedirect(redirectRequest, nil) == nil {
			t.Fatal("plaintext redirect was not rejected")
		}
		if got, want := options.Cloud.Services[cloud.ResourceManager].Endpoint, "https://"+rpHost; got != want {
			t.Fatalf("logical endpoint = %q, want %q", got, want)
		}
		credential := new(recordingCredential)
		client, err := containerservice.NewManagedClustersClient("subscription", credential, options)
		if err != nil {
			t.Fatalf("create managed clusters client: %v", err)
		}
		pager := client.NewListByResourceGroupPager("resource-group", nil)
		for pager.More() {
			if _, err := pager.NextPage(context.Background()); err != nil {
				t.Fatalf("list managed clusters page: %v", err)
			}
		}
		if got := requests.Load(); got != 2 {
			t.Fatalf("request count = %d, want 2", got)
		}
		wantScope := cloud.AzurePublic.Services[cloud.ResourceManager].Audience + "/.default"
		if len(credential.requests) != 1 || len(credential.requests[0].Scopes) != 1 || credential.requests[0].Scopes[0] != wantScope {
			t.Fatalf("token requests = %#v, want one request for %q", credential.requests, wantScope)
		}
	})

	t.Run("rejects untrusted ingress server", func(t *testing.T) {
		assertIngressTLSRejected(t, rpHost, false)
	})

	t.Run("rejects wrong ingress DNS identity", func(t *testing.T) {
		assertIngressTLSRejected(t, "wrong.example.com", true)
	})
}

func assertIngressTLSRejected(t *testing.T, serverDNSName string, trustServerCertificate bool) {
	t.Helper()
	clearRPIngressEnvironment(t)
	var requests atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cluster","name":"cluster","type":"Microsoft.ContainerService/managedClusters","location":"eastus2","properties":{}}`))
	}))
	serverTLS, serverCAPEM := serverTLSConfig(t, serverDNSName)
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()

	certPath := filepath.Join(t.TempDir(), "client.pem")
	if err := os.WriteFile(certPath, clientCertificatePEM(t), 0o600); err != nil {
		t.Fatalf("write client certificate: %v", err)
	}
	if !trustServerCertificate {
		_, serverCAPEM = serverTLSConfig(t, rpIngressHost)
	}
	caPath := filepath.Join(t.TempDir(), "server-ca.pem")
	if err := os.WriteFile(caPath, serverCAPEM, 0o600); err != nil {
		t.Fatalf("write server CA certificate: %v", err)
	}
	t.Setenv("RP_URL", server.URL)
	t.Setenv("RP_HOST", rpIngressHost)
	t.Setenv("RP_CLIENT_CERT_PATH", certPath)
	t.Setenv("RP_SERVER_CA_CERT_PATH", caPath)

	options, err := containerServiceClientOptions(cloud.AzurePublic)
	if err != nil {
		t.Fatalf("build client options: %v", err)
	}
	client, err := containerservice.NewManagedClustersClient("subscription", new(recordingCredential), options)
	if err != nil {
		t.Fatalf("create managed clusters client: %v", err)
	}
	if _, err := client.Get(context.Background(), "resource-group", "cluster", nil); err == nil {
		t.Fatal("expected TLS authentication to reject ingress server")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("authenticated HTTP request count = %d, want 0", got)
	}
}

func TestMachineAgentPoolName(t *testing.T) {
	t.Run("managed NAP override", func(t *testing.T) {
		t.Setenv("AKS_MACHINES_POOL_NAME", "aksmanagedap")
		if got := machineAgentPoolName(true); got != "aksmanagedap" {
			t.Fatalf("machine pool = %q, want aksmanagedap", got)
		}
	})
	t.Run("self-hosted default", func(t *testing.T) {
		t.Setenv("AKS_MACHINES_POOL_NAME", "")
		if got := machineAgentPoolName(true); got != "testmpool" {
			t.Fatalf("machine pool = %q, want testmpool", got)
		}
	})
	t.Run("out-of-cluster default", func(t *testing.T) {
		t.Setenv("AKS_MACHINES_POOL_NAME", "")
		if got := machineAgentPoolName(false); got != "aksmanagedap" {
			t.Fatalf("machine pool = %q, want aksmanagedap", got)
		}
	})
}

func clearRPIngressEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"RP_URL", "RP_HOST", "RP_CLIENT_CERT_PATH", "RP_SERVER_CA_CERT_PATH"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
	}
}

func serverTLSConfig(t *testing.T, dnsName string) (*tls.Config, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: dnsName},
		DNSNames:              []string{dnsName},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create server certificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("parse server certificate: %v", err)
	}
	return &tls.Config{ //nolint:gosec // test server validates client-certificate presence only
		Certificates: []tls.Certificate{certificate},
		ClientAuth:   tls.RequireAnyClientCert,
	}, certPEM
}

func clientCertificatePEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "e2e-client"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return append(certPEM, keyPEM...)
}

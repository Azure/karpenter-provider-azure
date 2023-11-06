// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package opts

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"github.com/Azure/go-armbalancer"
)

var defaultHTTPClient *http.Client

func init() {
	defaultHTTPClient = &http.Client{
		// For Now using the defaults recommended by Track 2
		Transport: armbalancer.New(armbalancer.Options{
			PoolSize: 100,
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   30 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
				TLSClientConfig: &tls.Config{
					MinVersion: tls.VersionTLS12,
				},
			},
		}),
	}
}

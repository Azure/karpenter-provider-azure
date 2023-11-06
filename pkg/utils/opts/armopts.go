// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package opts

import (
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/karpenter/pkg/auth"
)

func DefaultArmOpts() *arm.ClientOptions {
	opts := &arm.ClientOptions{}
	opts.Telemetry = DefaultTelemetryOpts()
	opts.Retry = DefaultRetryOpts()
	opts.Transport = defaultHTTPClient
	return opts
}

func DefaultRetryOpts() policy.RetryOptions {
	return policy.RetryOptions{
		MaxRetries: 20,
		// Note the default retry behavior is exponential backoff
		RetryDelay: time.Second * 5,
		// TODO: bsoghigian: Investigate if we want to leverage some of the status codes other than the defaults.
		// the defaults are // StatusCodes specifies the HTTP status codes that indicate the operation should be retried.
		// A nil slice will use the following values.
		//   http.StatusRequestTimeout      408
		//   http.StatusTooManyRequests     429
		//   http.StatusInternalServerError 500
		//   http.StatusBadGateway          502
		//   http.StatusServiceUnavailable  503
		//   http.StatusGatewayTimeout      504
		// Specifying values will replace the default values.
		// Specifying an empty slice will disable retries for HTTP status codes.
		// StatusCodes: nil,
	}
}

func DefaultHTTPClient() *http.Client {
	return defaultHTTPClient
}

func DefaultTelemetryOpts() policy.TelemetryOptions {
	return policy.TelemetryOptions{
		ApplicationID: auth.GetUserAgentExtension(),
	}
}

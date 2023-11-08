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

package opts

import (
	"net/http"

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
		MaxRetries: -1,
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

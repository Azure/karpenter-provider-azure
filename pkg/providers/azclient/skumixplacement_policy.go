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

package azclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const skuMixPlacementInstanceDescriptionType = "VMSizes"

var _ policy.Policy = &skuMixPlacementRequestPolicy{}

// TODO: Should be able to delete this at the beginning of sept
// armrecommender v0.2.0 omits the required instanceDescription.type discriminator.
type skuMixPlacementRequestPolicy struct{}

func (p *skuMixPlacementRequestPolicy) Do(req *policy.Request) (*http.Response, error) {
	if req.Raw().Method != http.MethodPost || req.Body() == nil {
		return req.Next()
	}
	if _, err := req.Body().Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewinding SKU Mix Placement request body: %w", err)
	}
	body, err := io.ReadAll(req.Body())
	if err != nil {
		return nil, fmt.Errorf("reading SKU Mix Placement request body: %w", err)
	}
	body, err = addSKUMixPlacementInstanceDescriptionType(body)
	if err != nil {
		return nil, err
	}
	if err := req.SetBody(&readSeekCloser{Reader: bytes.NewReader(body)}, "application/json"); err != nil {
		return nil, fmt.Errorf("setting SKU Mix Placement request body: %w", err)
	}
	return req.Next()
}

func skuMixPlacementClientOptions(options *arm.ClientOptions) *arm.ClientOptions {
	result := *options
	result.PerCallPolicies = append([]policy.Policy(nil), options.PerCallPolicies...)
	result.PerCallPolicies = append(result.PerCallPolicies, &skuMixPlacementRequestPolicy{})
	return &result
}

func addSKUMixPlacementInstanceDescriptionType(body []byte) ([]byte, error) {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("unmarshalling SKU Mix Placement request: %w", err)
	}
	var instanceDescription map[string]json.RawMessage
	if err := json.Unmarshal(request["instanceDescription"], &instanceDescription); err != nil {
		return nil, fmt.Errorf("unmarshalling SKU Mix Placement instance description: %w", err)
	}
	instanceDescription["type"] = json.RawMessage(`"` + skuMixPlacementInstanceDescriptionType + `"`)
	encodedInstanceDescription, err := json.Marshal(instanceDescription)
	if err != nil {
		return nil, fmt.Errorf("marshaling SKU Mix Placement instance description: %w", err)
	}
	request["instanceDescription"] = encodedInstanceDescription
	result, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshaling SKU Mix Placement request: %w", err)
	}
	return result, nil
}

type readSeekCloser struct {
	*bytes.Reader
}

func (r *readSeekCloser) Close() error {
	return nil
}

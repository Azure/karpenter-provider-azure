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

package instance

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	arg "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient"
)

func NewQueryRequest(subscriptionID *string, query string) *arg.QueryRequest {
	return &arg.QueryRequest{
		Query: &query,
		Options: &arg.QueryRequestOptions{
			ResultFormat: to.Ptr(arg.ResultFormatObjectArray),
		},
		Subscriptions: []*string{subscriptionID},
	}
}

// Queries Azure Resource Graph using Resources() and returns a list of all pages of data.
func GetResourceData(ctx context.Context, client azclient.AzureResourceGraphAPI, req arg.QueryRequest) ([]Resource, error) {
	dataRemaining := true // used to handle ARG responses > 1 page long
	var data []Resource
	for dataRemaining {
		resp, err := client.Resources(ctx, req, nil)
		if err != nil {
			return nil, err
		}
		interfaceArray, ok := resp.Data.([]interface{})
		if !ok {
			return nil, fmt.Errorf("type casting query response as interface array failed")
		}
		for i := range interfaceArray {
			switch resource := interfaceArray[i].(type) {
			case map[string]interface{}:
				data = append(data, resource)
			}
		}
		dataRemaining = false
		if resp.SkipToken != nil {
			req.Options.SkipToken = resp.SkipToken
			dataRemaining = true
		}
	}
	return data, nil
}

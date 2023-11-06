// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package instance

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	arg "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
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
func GetResourceData(ctx context.Context, client AzureResourceGraphAPI, req arg.QueryRequest) ([]Resource, error) {
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

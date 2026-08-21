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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armrecommender"
	. "github.com/onsi/gomega"
)

func TestAddSKUMixPlacementInstanceDescriptionType(t *testing.T) {
	g := NewWithT(t)
	body := []byte(`{
		"zones":["1","2"],
		"capacityProfile":{"capacity":1},
		"instanceDescription":{"vmSizes":[{"name":"Standard_D2s_v5","rank":0}]}
	}`)

	result, err := addSKUMixPlacementInstanceDescriptionType(body)
	g.Expect(err).NotTo(HaveOccurred())

	var request struct {
		Zones               []string `json:"zones"`
		InstanceDescription struct {
			Type    string `json:"type"`
			VMSizes []struct {
				Name string `json:"name"`
				Rank int32  `json:"rank"`
			} `json:"vmSizes"`
		} `json:"instanceDescription"`
	}
	g.Expect(json.Unmarshal(result, &request)).To(Succeed())
	g.Expect(request.Zones).To(Equal([]string{"1", "2"}))
	g.Expect(request.InstanceDescription.Type).To(Equal("VMSizes"))
	g.Expect(request.InstanceDescription.VMSizes).To(Equal([]struct {
		Name string `json:"name"`
		Rank int32  `json:"rank"`
	}{{Name: "Standard_D2s_v5", Rank: 0}}))
}

func TestAddSKUMixPlacementInstanceDescriptionTypeRejectsInvalidRequest(t *testing.T) {
	g := NewWithT(t)
	_, err := addSKUMixPlacementInstanceDescriptionType([]byte(`{"zones":[]}`))
	g.Expect(err).To(MatchError(ContainSubstring("unmarshalling SKU Mix Placement instance description")))
}

func TestSKUMixPlacementClientOptionsDoesNotMutateSharedOptions(t *testing.T) {
	g := NewWithT(t)
	existingPolicy := &spotSystemNodePolicy{}
	options := &arm.ClientOptions{}
	options.PerCallPolicies = []policy.Policy{existingPolicy}

	result := skuMixPlacementClientOptions(options)

	g.Expect(options.PerCallPolicies).To(Equal([]policy.Policy{existingPolicy}))
	g.Expect(result.PerCallPolicies).To(HaveLen(2))
	g.Expect(result.PerCallPolicies[0]).To(BeIdenticalTo(existingPolicy))
	g.Expect(result.PerCallPolicies[1]).To(BeAssignableToTypeOf(&skuMixPlacementRequestPolicy{}))
}

func TestSKUMixPlacementClientAddsInstanceDescriptionTypeToSerializedRequest(t *testing.T) {
	g := NewWithT(t)
	transport := &captureTransport{}
	options := skuMixPlacementClientOptions(&arm.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: transport},
	})
	client, err := armrecommender.NewSKUMixPlacementScoresClient(
		"00000000-0000-0000-0000-000000000000",
		staticTokenCredential{},
		options,
	)
	g.Expect(err).NotTo(HaveOccurred())

	_, err = client.Post(context.Background(), "westus2", armrecommender.SKUMixPlacementRequest{
		Zones: []*string{to.Ptr("1"), to.Ptr("2")},
		CapacityProfile: &armrecommender.SKUMixPlacementCapacityProfile{
			Capacity:           to.Ptr(int32(1)),
			CapacityType:       to.Ptr(armrecommender.SKUMixPlacementCapacityTypeVM),
			Priority:           to.Ptr(armrecommender.SKUMixPlacementPriorityRegular),
			AllocationStrategy: to.Ptr(armrecommender.SKUMixPlacementAllocationStrategyPrioritized),
			OSType:             to.Ptr(armrecommender.SKUMixPlacementOSTypeLinux),
		},
		InstanceDescription: &armrecommender.SKUMixPlacementInstanceDescription{
			VMSizes: []*armrecommender.SKUMixPlacementVMSize{
				{Name: to.Ptr("Standard_D2s_v5"), Rank: to.Ptr(int32(0))},
			},
		},
	}, nil)
	g.Expect(err).NotTo(HaveOccurred())

	var body struct {
		InstanceDescription struct {
			Type    string `json:"type"`
			VMSizes []struct {
				Name string `json:"name"`
				Rank int32  `json:"rank"`
			} `json:"vmSizes"`
		} `json:"instanceDescription"`
	}
	g.Expect(json.Unmarshal(transport.body, &body)).To(Succeed())
	g.Expect(body.InstanceDescription.Type).To(Equal("VMSizes"))
	g.Expect(body.InstanceDescription.VMSizes).To(Equal([]struct {
		Name string `json:"name"`
		Rank int32  `json:"rank"`
	}{{Name: "Standard_D2s_v5", Rank: 0}}))
}

type staticTokenCredential struct{}

func (staticTokenCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type captureTransport struct {
	body []byte
}

func (t *captureTransport) Do(request *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	t.body = body
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"placementChoices":[],"partialFulfillmentReason":"None"}`)),
		Request:    request,
	}, nil
}

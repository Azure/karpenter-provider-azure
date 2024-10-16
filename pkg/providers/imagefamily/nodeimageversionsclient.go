package imagefamily

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type NodeImageVersionsClient struct {
	cred azcore.TokenCredential
}

func NewNodeImageVersionsClient(cred azcore.TokenCredential) *NodeImageVersionsClient {
	return &NodeImageVersionsClient{
		cred: cred,
	}
}

func (l *NodeImageVersionsClient) List(ctx context.Context, location, subscription string) (NodeImageVersionsResponse, error) {
	resourceURL := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/providers/Microsoft.ContainerService/locations/%s/nodeImageVersions?api-version=%s",
		subscription, location, "2024-04-02-preview",
	)

	token, err := l.cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	if err != nil {
		return NodeImageVersionsResponse{}, err
	}

	req, err := http.NewRequestWithContext(context.Background(), "GET", resourceURL, nil)
	if err != nil {
		return NodeImageVersionsResponse{}, err
	}

	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return NodeImageVersionsResponse{}, err
	}
	defer resp.Body.Close()

	var response NodeImageVersionsResponse
	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&response)
	if err != nil {
		return NodeImageVersionsResponse{}, err
	}
	return response, nil
}

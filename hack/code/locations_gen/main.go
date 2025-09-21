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

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/samber/lo"
)

func main() {
	flag.Parse()
	if flag.NArg() != 1 {
		log.Fatalf("Usage: %s pkg/fake/locations.json", os.Args[0])
	}

	generateLocations(flag.Arg(0))
}

func generateLocations(filePath string) {
	ctx := context.Background()

	// Get subscription ID from environment variable
	subscriptionID := os.Getenv("AZURE_SUBSCRIPTION_ID")
	if subscriptionID == "" {
		log.Fatalf("AZURE_SUBSCRIPTION_ID environment variable is required")
	}

	// Create Azure credentials using default credential chain
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		log.Fatalf("Failed to create Azure credentials: %v", err)
	}

	// Create subscriptions client
	client, err := armsubscriptions.NewClient(cred, nil)
	if err != nil {
		log.Fatalf("Failed to create subscriptions client: %v", err)
	}

	// Get locations using the pager
	fmt.Printf("Fetching locations for subscription: %s\n", subscriptionID)
	pager := client.NewListLocationsPager(subscriptionID, nil)

	var locations []*armsubscriptions.Location

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			log.Fatalf("Failed to get next page of locations: %v", err)
		}

		locations = append(locations, page.Value...)
	}

	locations = lo.Filter(locations, func(location *armsubscriptions.Location, _ int) bool {
		return !strings.Contains(lo.FromPtr(location.Metadata.Geography), "Stage")
	})

	fmt.Printf("Found %d locations\n", len(locations))

	// Convert to JSON and save to file
	jsonData, err := json.MarshalIndent(locations, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal locations to JSON: %v", err)
	}

	// Redact subscription IDs from the JSON data
	redactedJSON := redactSubscriptionIDs(string(jsonData))
	// Append a newline character
	redactedJSON = redactedJSON + "\n"

	err = os.WriteFile(filePath, []byte(redactedJSON), 0644)
	if err != nil {
		log.Fatalf("Failed to write JSON data to file %s: %v", filePath, err)
	}

	fmt.Printf("Successfully saved locations to %s\n", filePath)
}

// redactSubscriptionIDs replaces subscription GUIDs in strings
func redactSubscriptionIDs(jsonContent string) string {
	// Regex pattern to match subscription GUIDs in paths
	// Matches: /subscriptions/{GUID}/
	subscriptionPattern := regexp.MustCompile(`/subscriptions/[0-9a-fA-F\-]+/`)

	// Replace with redacted subscription ID
	redacted := subscriptionPattern.ReplaceAllString(jsonContent, "/subscriptions/00000000-0000-0000-0000-000000000000/")

	return redacted
}

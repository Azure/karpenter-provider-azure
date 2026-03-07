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
	"testing"

	. "github.com/onsi/gomega"
)

func TestAZClientManager_DefaultSubscription(t *testing.T) {
	g := NewWithT(t)

	defaultClients := &SubscriptionClients{
		VirtualMachinesClient: nil, // ok for test
	}
	mgr := &AZClientManager{
		defaultSubscriptionID: "sub-default",
		defaultClients:        defaultClients,
		clients:               make(map[string]*SubscriptionClients),
	}

	// Empty subscription returns default
	clients, err := mgr.GetClients("")
	g.Expect(err).To(BeNil())
	g.Expect(clients).To(Equal(defaultClients))

	// Same subscription returns default
	clients, err = mgr.GetClients("sub-default")
	g.Expect(err).To(BeNil())
	g.Expect(clients).To(Equal(defaultClients))
}

func TestAZClientManager_DefaultSubscriptionID(t *testing.T) {
	g := NewWithT(t)

	mgr := &AZClientManager{
		defaultSubscriptionID: "sub-123",
		clients:               make(map[string]*SubscriptionClients),
	}

	g.Expect(mgr.DefaultSubscriptionID()).To(Equal("sub-123"))
}

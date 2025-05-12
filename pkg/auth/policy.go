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

package auth

import (
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// AuxiliaryTokenPolicy provides a custom policy used to authenticate
// with shared node image galleries.
type AuxiliaryTokenPolicy struct {
	Token string
}

func (p *AuxiliaryTokenPolicy) Do(req *policy.Request) (*http.Response, error) {
	req.Raw().Header.Add("x-ms-authorization-auxiliary", "Bearer "+p.Token)
	return req.Next()
}

func NewAuxiliaryTokenPolicy(token azcore.AccessToken) AuxiliaryTokenPolicy {
	return AuxiliaryTokenPolicy{Token: token.Token}
}

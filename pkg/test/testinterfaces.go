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

// The intention of this file is to hold interfaces used in testing, where test code needs access to the Reset() method,
// while we don't want to expose Reset() in the production interface. Beyond that, the interfaces here should mirror their
// production counterparts. This allows us to keep the structs private and encapulated.
package test

import (
	"context"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
)

type TestNodeImageProvider interface {
	List(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) ([]imagefamily.NodeImage, error)
	Reset()
}

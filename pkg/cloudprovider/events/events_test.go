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

package events

import (
	"testing"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func TestNodeClaimMachineAPIValidationError(t *testing.T) {
	tests := []struct {
		name          string
		errorCode     string
		errorMessage  string
		expectedInMsg string
	}{
		{
			name:          "InvalidParameter error",
			errorCode:     "InvalidParameter",
			errorMessage:  "The taint key 'bad/key' is not valid.",
			expectedInMsg: "Machine API validation error (code=InvalidParameter): The taint key 'bad/key' is not valid.",
		},
		{
			name:          "ValidationError",
			errorCode:     "ValidationError",
			errorMessage:  "The request was invalid.",
			expectedInMsg: "Machine API validation error (code=ValidationError): The request was invalid.",
		},
		{
			name:          "PropertyChangeNotAllowed",
			errorCode:     "PropertyChangeNotAllowed",
			errorMessage:  "Changing property 'vmSize' is not allowed.",
			expectedInMsg: "Machine API validation error (code=PropertyChangeNotAllowed): Changing property 'vmSize' is not allowed.",
		},
		{
			name:          "long message is truncated",
			errorCode:     "InvalidParameter",
			errorMessage:  string(make([]byte, 600)),
			expectedInMsg: "...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			nodeClaim := &karpv1.NodeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-nodeclaim",
					UID:  types.UID("test-uid-123"),
				},
			}

			event := NodeClaimMachineAPIValidationError(nodeClaim, tt.errorCode, tt.errorMessage)

			g.Expect(event.Reason).To(Equal(MachineAPIValidationReason))
			g.Expect(event.Type).To(Equal("Warning"))
			g.Expect(event.InvolvedObject).To(Equal(nodeClaim))
			g.Expect(event.Message).To(ContainSubstring(tt.expectedInMsg))
			g.Expect(event.DedupeValues).To(ConsistOf("test-uid-123"))
		})
	}
}

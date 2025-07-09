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

package options

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestMapValue_NewMapValue(t *testing.T) {
	g := NewWithT(t)

	mapValue := NewMapValue()

	g.Expect(mapValue).To(BeEmpty())
	g.Expect(mapValue.String()).To(Equal(""))
}

func TestMapValue_Set(t *testing.T) {
	testCases := []struct {
		name           string
		input          string
		expectedResult MapValue
		expectedErrStr string
	}{
		{
			name:           "should parse a single key-value pair",
			input:          "key1=value1",
			expectedResult: MapValue{"key1": "value1"},
		},
		{
			name:  "should parse multiple key-value pairs",
			input: "key1=value1,key2=value2,key3=value3",
			expectedResult: MapValue{
				"key1": "value1",
				"key2": "value2",
				"key3": "value3",
			},
		},
		{
			name:  "should handle empty values",
			input: "key1=,key2=value2",
			expectedResult: MapValue{
				"key1": "",
				"key2": "value2",
			},
		},
		{
			name:  "should trim whitespace from keys and values",
			input: "  key1  =  value1  ,  key2=value2  ",
			expectedResult: MapValue{
				"key1": "value1",
				"key2": "value2",
			},
		},
		{
			name:  "should ignore empty entries",
			input: "key1=value1,,key2=value2",
			expectedResult: MapValue{
				"key1": "value1",
				"key2": "value2",
			},
		},
		{
			name:           "should handle empty string input",
			input:          "",
			expectedResult: MapValue{},
		},
		{
			name:           "should return error for missing equals sign",
			input:          "key1value1",
			expectedErrStr: "invalid key=value pair: \"key1value1\"",
		},
		{
			name:           "should return error for empty key",
			input:          "=value1",
			expectedErrStr: "empty key in pair: \"=value1\"",
		},
		{
			name:           "should return error for whitespace-only key",
			input:          "   =value1",
			expectedErrStr: "empty key in pair: \"   =value1\"",
		},
		{
			name:           "should return error for multiple equals signs",
			input:          "key1=value1=extra",
			expectedErrStr: "invalid key=value pair: \"key1=value1=extra\"",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			mapValue := NewMapValue()

			err := mapValue.Set(tc.input)

			if tc.expectedErrStr != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.expectedErrStr)))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(mapValue).To(Equal(tc.expectedResult))
			}
		})
	}
}

func TestMapValue_String(t *testing.T) {
	testCases := []struct {
		name           string
		input          string
		expectedResult string
	}{
		{
			name:           "should return empty string for empty map",
			input:          "",
			expectedResult: "",
		},
		{
			name:           "should return correct string for single entry",
			input:          "key1=value1",
			expectedResult: "key1=value1",
		},
		{
			name:           "should return comma-separated string for multiple entries",
			input:          "key1=value1,key2=value2",
			expectedResult: "key1=value1,key2=value2",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			mapValue := NewMapValue()

			g.Expect(mapValue.Set(tc.input)).To(Succeed())

			result := mapValue.String()
			g.Expect(result).To(Equal(tc.expectedResult))
		})
	}
}

func TestMapValue_Set_MultipleTimes(t *testing.T) {
	g := NewWithT(t)
	mapValue := NewMapValue()

	g.Expect(mapValue.Set("key1=value1,key2=value2")).To(Succeed())
	g.Expect(mapValue).To(HaveLen(2))

	g.Expect(mapValue.Set("key3=value3")).To(Succeed())
	g.Expect(mapValue).To(Equal(MapValue{"key3": "value3"}))
	g.Expect(mapValue).ToNot(HaveKey("key1"))
	g.Expect(mapValue).ToNot(HaveKey("key2"))
}

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

package client

import (
	"fmt"
)

type comparisonOperator string

const (
	Equals comparisonOperator = "eq"
)

// https://learn.microsoft.com/en-us/graph/filter-query-parameter?tabs=http
//
// Note: this is only for storing basic operators
type Filter struct {
	Field    string
	Operator comparisonOperator
	Value    string
}

func (f *Filter) String() string {
	return fmt.Sprintf("%s %s '%s'", f.Field, f.Operator, f.Value)
}

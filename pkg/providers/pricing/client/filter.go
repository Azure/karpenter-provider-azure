// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
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

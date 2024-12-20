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

package conversion

import (
	"reflect"
	"testing"
)

func TestOptions(t *testing.T) {
	got := &options{}
	WithPath("path")(got)

	want := &options{
		path: "path",
		// we can't compare wc as functions are not
		// comparable in golang (thus it needs to be
		// done indirectly)
	}

	if !reflect.DeepEqual(got, want) {
		t.Error("option was not applied")
	}
}

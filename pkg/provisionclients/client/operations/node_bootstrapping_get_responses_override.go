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

package operations

import (
	"io"

	"github.com/go-openapi/runtime"
	"github.com/go-openapi/strfmt"
)

func (o *NodeBootstrappingGetDefault) readResponse(response runtime.ClientResponse, _ runtime.Consumer, _ strfmt.Registry) error {
	// response payload

	// THIS IS MODIFIED FROM AUTO-GENERATED.
	// There is a known issue on the server side where content type in the header (JSON) doesn't match actual (text).
	// This is a work around for that, which should be safe due to the fact that string > JSON. Only affect logging format for now.
	// The "consumer" that we replaced only decode into JSON, and unlikely to change.
	rc := response.Body()
	defer rc.Close()

	bytes, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	o.Payload = string(bytes)

	// if err := consumer.Consume(response.Body(), &o.Payload); err != nil && err != io.EOF {
	// return err
	// }

	return nil
}

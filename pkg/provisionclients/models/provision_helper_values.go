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
// Code generated by go-swagger; DO NOT EDIT.

package models

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	"context"

	"github.com/go-openapi/errors"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/swag"
	"github.com/go-openapi/validate"
)

// ProvisionHelperValues provision helper values
//
// swagger:model ProvisionHelperValues
type ProvisionHelperValues struct {

	// sku CPU
	// Required: true
	SkuCPU *float64 `json:"skuCPU"`

	// sku memory
	// Required: true
	SkuMemory *float64 `json:"skuMemory"`
}

// Validate validates this provision helper values
func (m *ProvisionHelperValues) Validate(formats strfmt.Registry) error {
	var res []error

	if err := m.validateSkuCPU(formats); err != nil {
		res = append(res, err)
	}

	if err := m.validateSkuMemory(formats); err != nil {
		res = append(res, err)
	}

	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}

func (m *ProvisionHelperValues) validateSkuCPU(formats strfmt.Registry) error {

	if err := validate.Required("skuCPU", "body", m.SkuCPU); err != nil {
		return err
	}

	return nil
}

func (m *ProvisionHelperValues) validateSkuMemory(formats strfmt.Registry) error {

	if err := validate.Required("skuMemory", "body", m.SkuMemory); err != nil {
		return err
	}

	return nil
}

// ContextValidate validates this provision helper values based on context it is used
func (m *ProvisionHelperValues) ContextValidate(ctx context.Context, formats strfmt.Registry) error {
	return nil
}

// MarshalBinary interface implementation
func (m *ProvisionHelperValues) MarshalBinary() ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return swag.WriteJSON(m)
}

// UnmarshalBinary interface implementation
func (m *ProvisionHelperValues) UnmarshalBinary(b []byte) error {
	var res ProvisionHelperValues
	if err := swag.ReadJSON(b, &res); err != nil {
		return err
	}
	*m = res
	return nil
}

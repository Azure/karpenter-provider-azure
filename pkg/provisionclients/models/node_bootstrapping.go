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

// NodeBootstrapping node bootstrapping
//
// swagger:model NodeBootstrapping
type NodeBootstrapping struct {

	// cse
	// Required: true
	Cse *string `json:"cse"`

	// custom data
	// Required: true
	CustomData *string `json:"customData"`

	// os image config
	// Required: true
	OsImageConfig *AzureOSImageConfig `json:"osImageConfig"`

	// sig image config
	// Required: true
	SigImageConfig *SigImageConfig `json:"sigImageConfig"`
}

// Validate validates this node bootstrapping
func (m *NodeBootstrapping) Validate(formats strfmt.Registry) error {
	var res []error

	if err := m.validateCse(formats); err != nil {
		res = append(res, err)
	}

	if err := m.validateCustomData(formats); err != nil {
		res = append(res, err)
	}

	if err := m.validateOsImageConfig(formats); err != nil {
		res = append(res, err)
	}

	if err := m.validateSigImageConfig(formats); err != nil {
		res = append(res, err)
	}

	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}

func (m *NodeBootstrapping) validateCse(formats strfmt.Registry) error {

	if err := validate.Required("cse", "body", m.Cse); err != nil {
		return err
	}

	return nil
}

func (m *NodeBootstrapping) validateCustomData(formats strfmt.Registry) error {

	if err := validate.Required("customData", "body", m.CustomData); err != nil {
		return err
	}

	return nil
}

func (m *NodeBootstrapping) validateOsImageConfig(formats strfmt.Registry) error {

	if err := validate.Required("osImageConfig", "body", m.OsImageConfig); err != nil {
		return err
	}

	if m.OsImageConfig != nil {
		if err := m.OsImageConfig.Validate(formats); err != nil {
			if ve, ok := err.(*errors.Validation); ok {
				return ve.ValidateName("osImageConfig")
			} else if ce, ok := err.(*errors.CompositeError); ok {
				return ce.ValidateName("osImageConfig")
			}
			return err
		}
	}

	return nil
}

func (m *NodeBootstrapping) validateSigImageConfig(formats strfmt.Registry) error {

	if err := validate.Required("sigImageConfig", "body", m.SigImageConfig); err != nil {
		return err
	}

	if m.SigImageConfig != nil {
		if err := m.SigImageConfig.Validate(formats); err != nil {
			if ve, ok := err.(*errors.Validation); ok {
				return ve.ValidateName("sigImageConfig")
			} else if ce, ok := err.(*errors.CompositeError); ok {
				return ce.ValidateName("sigImageConfig")
			}
			return err
		}
	}

	return nil
}

// ContextValidate validate this node bootstrapping based on the context it is used
func (m *NodeBootstrapping) ContextValidate(ctx context.Context, formats strfmt.Registry) error {
	var res []error

	if err := m.contextValidateOsImageConfig(ctx, formats); err != nil {
		res = append(res, err)
	}

	if err := m.contextValidateSigImageConfig(ctx, formats); err != nil {
		res = append(res, err)
	}

	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}

func (m *NodeBootstrapping) contextValidateOsImageConfig(ctx context.Context, formats strfmt.Registry) error {

	if m.OsImageConfig != nil {

		if err := m.OsImageConfig.ContextValidate(ctx, formats); err != nil {
			if ve, ok := err.(*errors.Validation); ok {
				return ve.ValidateName("osImageConfig")
			} else if ce, ok := err.(*errors.CompositeError); ok {
				return ce.ValidateName("osImageConfig")
			}
			return err
		}
	}

	return nil
}

func (m *NodeBootstrapping) contextValidateSigImageConfig(ctx context.Context, formats strfmt.Registry) error {

	if m.SigImageConfig != nil {

		if err := m.SigImageConfig.ContextValidate(ctx, formats); err != nil {
			if ve, ok := err.(*errors.Validation); ok {
				return ve.ValidateName("sigImageConfig")
			} else if ce, ok := err.(*errors.CompositeError); ok {
				return ce.ValidateName("sigImageConfig")
			}
			return err
		}
	}

	return nil
}

// MarshalBinary interface implementation
func (m *NodeBootstrapping) MarshalBinary() ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return swag.WriteJSON(m)
}

// UnmarshalBinary interface implementation
func (m *NodeBootstrapping) UnmarshalBinary(b []byte) error {
	var res NodeBootstrapping
	if err := swag.ReadJSON(b, &res); err != nil {
		return err
	}
	*m = res
	return nil
}

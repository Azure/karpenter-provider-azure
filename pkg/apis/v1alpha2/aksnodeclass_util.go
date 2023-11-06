// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package v1alpha2

func (in *AKSNodeClassSpec) IsEmptyImageID() bool {
	return in.ImageID == nil || *in.ImageID == ""
}

func (in *AKSNodeClassSpec) GetImageVersion() string {
	if in.ImageVersion == nil {
		return ""
	}
	return *in.ImageVersion
}

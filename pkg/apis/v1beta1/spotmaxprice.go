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

package v1beta1

import (
	"fmt"
	"strconv"
	"strings"
)

// SpotMaxPriceFixed returns the SpotMaxPrice normalized to 1e-5 USD units (integer fixed-point).
// Returns nil, nil for a nil SpotMaxPrice.
// Returns -1 for the sentinel value "-1" (no price-based eviction).
// Returns the value * 100000 for valid positive decimal strings.
// Examples:
//
//	nil      -> nil, nil
//	"-1"     -> -1, nil
//	"2"      -> 200000, nil
//	"0.98765" -> 98765, nil
func (s *AKSNodeClassSpec) SpotMaxPriceFixed() (*int64, error) {
	if s.SpotMaxPrice == nil {
		return nil, nil
	}
	return ParseSpotMaxPrice(*s.SpotMaxPrice)
}

// ParseSpotMaxPrice parses a spot max price string into a fixed-point integer (USD * 100000).
// It accepts "-1" (sentinel) or positive decimal strings with up to 5 decimal places.
// It rejects zero, negatives other than -1, more than 5 decimal places, and non-numeric strings.
func ParseSpotMaxPrice(v string) (*int64, error) {
	v = strings.TrimSpace(v)
	if v == "-1" {
		x := int64(-1)
		return &x, nil
	}

	// Reject any leading minus (only -1 is allowed negative)
	if strings.HasPrefix(v, "-") {
		return nil, fmt.Errorf("spotMaxPrice %q must be \"-1\" or a value greater than 0", v)
	}

	parts := strings.SplitN(v, ".", 2)
	if len(parts) == 0 || parts[0] == "" {
		return nil, fmt.Errorf("invalid spotMaxPrice %q", v)
	}

	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid spotMaxPrice %q: %w", v, err)
	}

	frac := int64(0)
	if len(parts) == 2 {
		fracStr := parts[1]
		if len(fracStr) == 0 {
			return nil, fmt.Errorf("invalid spotMaxPrice %q: decimal point with no digits after", v)
		}
		if len(fracStr) > 5 {
			return nil, fmt.Errorf("spotMaxPrice %q has more than 5 decimal places", v)
		}
		// Right-pad to 5 digits
		for len(fracStr) < 5 {
			fracStr += "0"
		}
		frac, err = strconv.ParseInt(fracStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid spotMaxPrice %q: %w", v, err)
		}
	}

	fixed := whole*100000 + frac
	if fixed <= 0 {
		return nil, fmt.Errorf("spotMaxPrice must be \"-1\" or a value greater than 0, got %q", v)
	}
	result := fixed
	return &result, nil
}

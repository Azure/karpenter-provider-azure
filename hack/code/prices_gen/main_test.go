package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestWritePricingPreservesFloatPrecision(t *testing.T) {
	src := &bytes.Buffer{}
	writePricing(src, "prices", []string{"Standard_Test"}, "test-region", func(string) (float64, bool) {
		return 0.1234567, true
	})

	if !strings.Contains(src.String(), `"Standard_Test":0.1234567,`) {
		t.Fatalf("expected round-trip-safe price literal, got %q", src.String())
	}
}

// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package auth

import (
	"crypto/rsa"
	"crypto/x509"
	"fmt"

	"golang.org/x/crypto/pkcs12"

	"github.com/Azure/karpenter/pkg/utils/project"
)

// decodePkcs12 decodes a PKCS#12 client certificate by extracting the public certificate and
// the private RSA key
func decodePkcs12(pkcs []byte, password string) (*x509.Certificate, *rsa.PrivateKey, error) {
	privateKey, certificate, err := pkcs12.Decode(pkcs, password)
	if err != nil {
		return nil, nil, fmt.Errorf("decoding the PKCS#12 client certificate: %w", err)
	}
	rsaPrivateKey, isRsaKey := privateKey.(*rsa.PrivateKey)
	if !isRsaKey {
		return nil, nil, fmt.Errorf("PKCS#12 certificate must contain a RSA private key")
	}

	return certificate, rsaPrivateKey, nil
}

func GetUserAgentExtension() string {
	return fmt.Sprintf("karpenter-aks/v%s", project.Version)
}

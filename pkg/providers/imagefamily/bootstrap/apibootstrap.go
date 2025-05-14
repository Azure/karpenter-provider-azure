package bootstrap

import (
	"encoding/base64"
)

var _ Bootstrapper = (*APIbootstrap)(nil) // assert AKS implements Bootstrapper

type APIbootstrap struct {
	UserData *string
}

func (a APIbootstrap) Script() (string, error) {
	if a.UserData == nil {
		return "", nil
	}

	return base64.StdEncoding.EncodeToString([]byte(*a.UserData)), nil
}

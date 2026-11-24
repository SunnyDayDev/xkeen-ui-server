package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/validation"
)

var errValidatorOptions = errors.New("invalid Xray validator configuration: use an absolute socket or an HTTPS origin with dedicated CA and mTLS credentials")

type validatorConfig struct {
	socket, origin, ca, cert, key string
}

func (v validatorConfig) valid() bool {
	if v.socket != "" {
		return filepath.IsAbs(v.socket) && v.origin == "" && v.ca == "" && v.cert == "" && v.key == ""
	}
	return v.origin != "" && v.ca != "" && v.cert != "" && v.key != ""
}

func (v validatorConfig) open() (publication.CatalogValidator, error) {
	if v.socket != "" {
		return socketValidator(v.socket), nil
	}
	if v.origin == "" {
		return nil, nil
	}
	pem, err := os.ReadFile(v.ca)
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("invalid Xray validator trust: cannot load dedicated CA")
	}
	pair, err := tls.LoadX509KeyPair(v.cert, v.key)
	if err != nil {
		return nil, errors.New("invalid Xray validator trust: cannot load client credentials")
	}
	client, err := validation.Remote(v.origin, &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{pair}})
	if err != nil {
		return nil, errValidatorOptions
	}
	return client, nil
}

type socketValidator string

func (s socketValidator) Validate(ctx context.Context, profile string, files []configuration.CatalogFile) error {
	client, err := validation.Local(string(s))
	if err != nil {
		return err
	}
	return client.Validate(ctx, profile, files)
}

package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"strings"
	"syscall"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/validation"
)

const (
	validatorCAPath   = serverConfig + "/validator-ca.pem"
	validatorCertPath = serverConfig + "/validator-client-cert.pem"
	validatorKeyPath  = serverConfig + "/validator-client-key.pem"
)

var errValidator = errors.New("invalid external validator trust or HTTPS origin")

type externalValidationProfile struct {
	Origin, CADigest, CertDigest, KeyDigest string
}

type externalValidationSources struct {
	profile       externalValidationProfile
	ca, cert, key []byte
}

func readExternalValidation(origin, caPath, certPath, keyPath string) (externalValidationSources, error) {
	if origin == "" && caPath == "" && certPath == "" && keyPath == "" {
		return externalValidationSources{}, nil
	}
	if origin == "" || caPath == "" || certPath == "" || keyPath == "" {
		return externalValidationSources{}, errors.New("complete external validator configuration is required")
	}
	ca, err := readProtected(caPath, 0, 1<<20)
	if err != nil {
		return externalValidationSources{}, errValidator
	}
	cert, err := readProtected(certPath, 0, 1<<20)
	if err != nil {
		return externalValidationSources{}, errValidator
	}
	key, err := readProtected(keyPath, 0600, 1<<20)
	if err != nil {
		return externalValidationSources{}, errValidator
	}
	origin = strings.TrimSuffix(origin, "/")
	if err := validateExternalTrust(origin, ca, cert, key); err != nil {
		clear(key)
		return externalValidationSources{}, err
	}
	return externalValidationSources{
		profile: externalValidationProfile{origin, digest(ca), digest(cert), digest(key)},
		ca:      ca, cert: cert, key: key,
	}, nil
}

func validateExternalTrust(origin string, ca, cert, key []byte) error {
	roots := x509.NewCertPool()
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil || !roots.AppendCertsFromPEM(ca) {
		return errValidator
	}
	if _, err := validation.Remote(origin, &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{pair}}); err != nil {
		return errValidator
	}
	return nil
}

func (s externalValidationSources) persist(gid uint32) error {
	if s.profile.Origin == "" {
		return nil
	}
	if err := writeFile(validatorCAPath, s.ca, 0644, 0, 0); err != nil {
		return err
	}
	if err := writeFile(validatorCertPath, s.cert, 0644, 0, 0); err != nil {
		return err
	}
	return writeFile(validatorKeyPath, s.key, 0640, 0, int(gid))
}

func (p externalValidationProfile) structurallyValid() bool {
	if p.Origin == "" {
		return p == (externalValidationProfile{})
	}
	return shaPattern.MatchString(p.CADigest) && shaPattern.MatchString(p.CertDigest) && shaPattern.MatchString(p.KeyDigest)
}

func (p externalValidationProfile) arguments(gid uint32) ([]string, error) {
	if p.Origin == "" {
		return nil, nil
	}
	ca, err := readProtected(validatorCAPath, 0644, 1<<20)
	if err != nil || digest(ca) != p.CADigest {
		return nil, errValidator
	}
	cert, err := readProtected(validatorCertPath, 0644, 1<<20)
	if err != nil || digest(cert) != p.CertDigest {
		return nil, errValidator
	}
	keyFile, err := checkedFile(validatorKeyPath, 0, 0640)
	if err != nil {
		return nil, errValidator
	}
	defer keyFile.Close()
	info, err := keyFile.Stat()
	if err != nil || info.Sys().(*syscall.Stat_t).Gid != gid {
		return nil, errValidator
	}
	key, err := readBounded(keyFile, 1<<20)
	defer clear(key)
	if err != nil || digest(key) != p.KeyDigest || validateExternalTrust(p.Origin, ca, cert, key) != nil {
		return nil, errValidator
	}
	return []string{"--validator-origin", p.Origin, "--validator-ca", validatorCAPath,
		"--validator-client-cert", validatorCertPath, "--validator-client-key", validatorKeyPath}, nil
}

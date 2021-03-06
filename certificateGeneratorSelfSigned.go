package talisman

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"github.com/rs/zerolog/log"
	"math/big"
	"os"
	"strings"
	"syscall"
	"time"
)

type SelfSignedCertificateGenerator struct {
	CertificateBasePath string
	CertificateValidity time.Duration
}

func (c SelfSignedCertificateGenerator) Renew(RenewRequest) (*RenewResponse, error) {
	panic("implement me")
}

func (c SelfSignedCertificateGenerator) Revoke(RevokeRequest) (*RevokeResponse, error) {
	panic("implement me")
}

func (c SelfSignedCertificateGenerator) Generate(request GenerateRequest) (*GenerateResponse, error) {
	// create certs directory
	certBasePath := strings.TrimRight(c.CertificateBasePath, string(os.PathSeparator))
	DebugVerbose("creating certs directory in " + certBasePath)
	_, err := os.Stat(certBasePath)
	if err != nil {
		if (err.(*os.PathError)).Err == syscall.ENOENT {
			// create the dir
			err := os.Mkdir(certBasePath, 0740)
			if err != nil {
				log.Error().Err(err).Msgf("could not create certs directory:")
				return nil, err
			}
		} else {
			log.Error().Err(err).Msgf("could not create certs directory:")
			return nil, err
		}
	}
	certPath := strings.Join([]string{certBasePath, request.CommonName + ".pem"}, string(os.PathSeparator))
	DebugVerbose("cert path: " + certPath)
	keyPath := strings.Join([]string{certBasePath, request.CommonName + "-key.pem"}, string(os.PathSeparator))
	DebugVerbose("key path: " + keyPath)

	DebugVerbose("checking if cert exists")
	_, err = os.Stat(certPath)
	if err != nil {
		if (err.(*os.PathError)).Err == syscall.ENOENT {
			// should't exist
			DebugVerbose("cert does not exist")
		} else {
			log.Error().Err(err).Msgf("could not create public key:")
			return nil, err
		}
	} else {
		DebugVerbose("cert exists")
		return nil, CertificateExistsErr{CommonName: request.CommonName}
	}
	DebugVerbose("checking if key exists")
	_, err = os.Stat(keyPath)
	if err != nil {
		if (err.(*os.PathError)).Err == syscall.ENOENT {
			// should't exist
			DebugVerbose("key does not exist")
		} else {
			log.Error().Err(err).Msgf("could not create private key:")
			return nil, err
		}
	} else {
		DebugVerbose("key exists")
		return nil, KeyExistsErr{CommonName: request.CommonName}
	}

	notBefore := time.Now()
	notAfter := notBefore.Add(c.CertificateValidity)

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	DebugVerbose("creating serial number with limit " + serialNumberLimit.String())
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		log.Error().Err(err).Msgf("failed to generate serial number:")
		return nil, err
	}
	DebugVerbose("serial number: " + serialNumber.String())

	template := x509.Certificate{
		DNSNames:     []string{request.CommonName},
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Ionoverse"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		EmailAddresses:        []string{request.AdminEmail},
	}

	hosts := []string{request.CommonName}
	template.DNSNames = hosts
	template.Subject.CommonName = request.CommonName

	template.IsCA = false
	template.KeyUsage |= x509.KeyUsageKeyEncipherment
	template.KeyUsage |= x509.KeyUsageCertSign
	template.KeyUsage |= x509.KeyUsageCRLSign

	// select the algorithm for the private key based on the request
	var theKey interface{}
	var theDerBytes []byte
	switch request.Algorithm {
	case ECDSA:
		DebugVerbose("generating ECDSA key")
		curve, err := determineECDSACurve(request.KeySize)
		if err != nil {
			log.Error().Err(err)
			return nil, err
		}
		key, err := ecdsa.GenerateKey(curve, rand.Reader)
		if err != nil {
			log.Error().Err(err).Msgf("failed to generate ECDSA private key:")
			return nil, err
		}
		DebugVerbose("creating cert")
		derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
		if err != nil {
			log.Error().Err(err).Msgf("failed to generate ECDSA certificate:")
			return nil, err
		}
		theKey = key
		theDerBytes = derBytes
	case RSA:
		DebugVerbose("generating RSA key")
		key, err := rsa.GenerateKey(rand.Reader, request.KeySize)
		if err != nil {
			log.Error().Err(err).Msgf("failed to generate RSA private key:")
			return nil, err
		}
		DebugVerbose("creating cert")
		derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
		if err != nil {
			log.Error().Err(err).Msgf("failed to generate RSA certificate:")
			return nil, err
		}
		theKey = key
		theDerBytes = derBytes
	default:
		// TODO typed error
		return nil, errors.New("unsupported algorithm")
	}

	// generate the certificate and key files
	DebugVerbose("opening cert file for writing")
	certOut, err := os.Create(certPath)
	if err != nil {
		log.Error().Err(err).Msgf("failed to open cert for writing:")
		return nil, err
	}
	DebugVerbose("encoding PEM cert")
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: theDerBytes}); err != nil {
		log.Error().Err(err).Msgf("failed to write data to cert:")
		return nil, err
	}
	if err := certOut.Close(); err != nil {
		log.Error().Err(err).Msgf("error closing cert:")
	}

	DebugVerbose("opening key for writing")
	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		log.Error().Err(err).Msgf("failed to open key for writing:")
		return nil, err
	}
	DebugVerbose("creating PEM block for key")
	pemBlock, err := pemBlockForKey(theKey)
	if err != nil {
		log.Error().Err(err).Msgf("failed to get pem key block:")
		return nil, err
	}
	DebugVerbose("encoding PEM key")
	if err := pem.Encode(keyOut, pemBlock); err != nil {
		log.Error().Err(err).Msgf("failed to write data to key:")
		return nil, err
	}
	if err := keyOut.Close(); err != nil {
		log.Error().Err(err).Msgf("error closing key:")
		return nil, err
	}
	return &GenerateResponse{
		KeyPath:  keyPath,
		CertPath: certPath,
	}, nil
}

// determineECDSACurve returns the correct curve for a given ECDSA key size.
func determineECDSACurve(keySize int) (elliptic.Curve, error) {
	switch keySize {
	case 224:
		return elliptic.P224(), nil
	case 256:
		return elliptic.P256(), nil
	case 384:
		return elliptic.P384(), nil
	case 521:
		return elliptic.P521(), nil
	default:
		return nil, InvalidKeySizeError{}
	}
}

func pemBlockForKey(key interface{}) (*pem.Block, error) {
	switch k := key.(type) {
	case *rsa.PrivateKey:
		return &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}, nil
	case *ecdsa.PrivateKey:
		b, err := x509.MarshalECPrivateKey(k)
		if err != nil {
			log.Error().Err(err).Msgf("Unable to marshal ECDSA private key:v")
			return nil, err
		}
		return &pem.Block{Type: "EC PRIVATE KEY", Bytes: b}, nil
	default:
		return nil, nil
	}
}

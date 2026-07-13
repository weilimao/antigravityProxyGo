//go:build windows

package patch

import (
	"crypto/x509"
	"encoding/pem"
	"syscall"
	"unsafe"
)

// getWindowsSystemCertificates 从 Windows 系统 ROOT 证书存储区中读取所有受信任根证书并导出为 PEM 格式
func getWindowsSystemCertificates() []byte {
	var combinedPEM []byte
	storeName, err := syscall.UTF16PtrFromString("ROOT")
	if err != nil {
		return nil
	}

	store, err := syscall.CertOpenSystemStore(0, storeName)
	if err != nil {
		return nil
	}
	defer syscall.CertCloseStore(store, 0)

	var certContext *syscall.CertContext
	for {
		certContext, err = syscall.CertEnumCertificatesInStore(store, certContext)
		if err != nil {
			break
		}
		if certContext == nil {
			break
		}

		certBytes := unsafe.Slice(certContext.EncodedCert, certContext.Length)
		cert, err := x509.ParseCertificate(certBytes)
		if err != nil {
			continue
		}

		pemBlock := &pem.Block{
			Type:  "CERTIFICATE",
			Bytes: cert.Raw,
		}
		pemBytes := pem.EncodeToMemory(pemBlock)
		if pemBytes != nil {
			combinedPEM = append(combinedPEM, pemBytes...)
		}
	}
	return combinedPEM
}

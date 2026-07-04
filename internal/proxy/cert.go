package proxy

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const maxCertCacheSize = 200

type certCacheEntry struct {
	cert     *tls.Certificate
	lastUsed time.Time
}

type CertManager struct {
	sync.RWMutex
	caCert        *x509.Certificate
	caPrivateKey  interface{}
	leafPrivateKey *rsa.PrivateKey
	certCache     map[string]*certCacheEntry
}

func NewCertManager() *CertManager {
	return &CertManager{
		certCache: make(map[string]*certCacheEntry),
	}
}

func (cm *CertManager) Init(caCertPath, caKeyPath string) error {
	cm.Lock()
	defer cm.Unlock()

	loadCA := func() error {
		// 1. Load CA Certificate
		caCertBytes, err := os.ReadFile(caCertPath)
		if err != nil {
			return fmt.Errorf("无法加载 CA 证书: %v", err)
		}

		block, _ := pem.Decode(caCertBytes)
		if block == nil || block.Type != "CERTIFICATE" {
			return errors.New("CA 证书 PEM 解码失败")
		}

		caCert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return fmt.Errorf("解析 CA 证书失败: %v", err)
		}
		cm.caCert = caCert

		// 2. Load CA Private Key
		caKeyBytes, err := os.ReadFile(caKeyPath)
		if err != nil {
			return fmt.Errorf("无法加载 CA 私钥: %v", err)
		}

		// Validate that the certificate and private key match
		if _, err := tls.X509KeyPair(caCertBytes, caKeyBytes); err != nil {
			return fmt.Errorf("CA 证书与私钥不匹配: %v", err)
		}

		keyBlock, _ := pem.Decode(caKeyBytes)
		if keyBlock == nil {
			return errors.New("CA 私钥 PEM 解码失败")
		}

		var caPrivKey interface{}
		if keyBlock.Type == "RSA PRIVATE KEY" {
			caPrivKey, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
		} else if keyBlock.Type == "PRIVATE KEY" {
			caPrivKey, err = x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
		} else {
			return fmt.Errorf("不支持的私钥类型: %s", keyBlock.Type)
		}

		if err != nil {
			return fmt.Errorf("解析 CA 私钥失败: %v", err)
		}
		cm.caPrivateKey = caPrivKey
		return nil
	}

	// Try loading existing CA
	err := loadCA()
	if err != nil {
		// Load failed (could be negative serial number or missing file), regenerate CA
		if genErr := GenerateCA(caCertPath, caKeyPath); genErr != nil {
			return fmt.Errorf("加载 CA 失败且重新生成失败: %v (原加载错误: %v)", genErr, err)
		}
		// Try loading again
		if reloadErr := loadCA(); reloadErr != nil {
			return fmt.Errorf("重新生成 CA 后加载失败: %v", reloadErr)
		}
	}

	// 3. Generate reusable leaf private key to optimize handshake performance
	leafPrivKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("生成叶子私钥失败: %v", err)
	}
	cm.leafPrivateKey = leafPrivKey
	cm.certCache = make(map[string]*certCacheEntry)

	return nil
}

func GenerateCA(caCertPath, caKeyPath string) error {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   "NodeMITMProxyCA",
			Organization: []string{"Node MITM Proxy CA"},
		},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour), // 10 years
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(caCertPath), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(caKeyPath), 0755); err != nil {
		return err
	}

	certOut, err := os.Create(caCertPath)
	if err != nil {
		return err
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return err
	}

	keyOut, err := os.OpenFile(caKeyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer keyOut.Close()
	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}); err != nil {
		return err
	}

	return nil
}

func (cm *CertManager) GetCertificate(host string) (*tls.Certificate, error) {
	cm.RLock()
	if entry, exists := cm.certCache[host]; exists {
		cm.RUnlock()
		// 更新 lastUsed 时间戳
		cm.Lock()
		entry.lastUsed = time.Now()
		cm.Unlock()
		return entry.cert, nil
	}
	cm.RUnlock()

	cm.Lock()
	defer cm.Unlock()

	// Double check cache
	if entry, exists := cm.certCache[host]; exists {
		entry.lastUsed = time.Now()
		return entry.cert, nil
	}

	if cm.caCert == nil || cm.caPrivateKey == nil || cm.leafPrivateKey == nil {
		return nil, errors.New("证书管理器未初始化或已失效")
	}

	// Generate serial number
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, fmt.Errorf("生成证书序列号失败: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   host,
			Organization: []string{"Node MITM Proxy CA"},
		},
		DNSNames:              []string{host},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, cm.caCert, &cm.leafPrivateKey.PublicKey, cm.caPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("签名叶子证书失败: %v", err)
	}

	tlsCert := &tls.Certificate{
		Certificate: [][]byte{derBytes},
		PrivateKey:  cm.leafPrivateKey,
	}

	// LRU 驱逐：超过上限时清理最旧的一半条目
	if len(cm.certCache) >= maxCertCacheSize {
		cm.evictOldest()
	}
	cm.certCache[host] = &certCacheEntry{cert: tlsCert, lastUsed: time.Now()}
	return tlsCert, nil
}

// evictOldest 清理缓存中最旧的一半条目，避免全量清空导致所有域名重新生成证书
func (cm *CertManager) evictOldest() {
	type hostTime struct {
		host string
		t    time.Time
	}
	entries := make([]hostTime, 0, len(cm.certCache))
	for h, e := range cm.certCache {
		entries = append(entries, hostTime{host: h, t: e.lastUsed})
	}
	// 按 lastUsed 升序排列
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].t.Before(entries[i].t) {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}
	// 删除最旧的一半
	half := len(entries) / 2
	for i := 0; i < half; i++ {
		delete(cm.certCache, entries[i].host)
	}
}

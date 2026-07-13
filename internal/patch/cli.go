package patch

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
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

// getCombinedCaPath 自动合并代理 CA 与本机的公网 CA，以避免覆盖 SSL_CERT_FILE 导致直连公网证书校验失败
func getCombinedCaPath(caPath string) string {
	caBytes, err := os.ReadFile(caPath)
	if err != nil {
		return caPath
	}

	var publicCaBytes []byte
	if runtime.GOOS == "windows" {
		publicCaBytes = getWindowsSystemCertificates()
	}

	// 如果通过系统 API 提取失败，退而尝试通过环境变量和已知路径查找
	if len(publicCaBytes) == 0 {
		sslCertFile := os.Getenv("SSL_CERT_FILE")

		// 优先读取当前环境变量设置的公网证书包（排除指向自身以防循环）
		if sslCertFile != "" && !strings.Contains(sslCertFile, "ca_combined.pem") && sslCertFile != caPath {
			if bytes, err := os.ReadFile(sslCertFile); err == nil {
				publicCaBytes = bytes
			}
		}
	}

	if len(publicCaBytes) == 0 {
		// 计算代理安装根目录（caPath 为 D:\antigravityProxy\data\certs\certs\ca.pem，往上推 4 级为安装根目录）
		installDir := caPath
		for i := 0; i < 4; i++ {
			installDir = filepath.Dir(installDir)
		}
		commonPaths := []string{
			filepath.Join(installDir, "cacert.pem"),
			"E:\\Conda\\Library\\ssl\\cacert.pem",
			"C:\\ProgramData\\Anaconda3\\Library\\ssl\\cacert.pem",
			"C:\\Miniconda3\\Library\\ssl\\cacert.pem",
		}
		for _, p := range commonPaths {
			if bytes, err := os.ReadFile(p); err == nil {
				publicCaBytes = bytes
				break
			}
		}
	}

	if len(publicCaBytes) == 0 {
		return caPath
	}

	combinedPath := filepath.Join(filepath.Dir(caPath), "ca_combined.pem")
	var combinedContent []byte
	combinedContent = append(combinedContent, caBytes...)
	combinedContent = append(combinedContent, []byte("\n")...)
	combinedContent = append(combinedContent, publicCaBytes...)

	if err := os.WriteFile(combinedPath, combinedContent, 0644); err == nil {
		return combinedPath
	}
	return caPath
}

func getCliCandidates(appData, homeDir string) []string {
	var candidates []string

	if runtime.GOOS == "windows" {
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			localAppData = filepath.Join(filepath.Dir(appData), "Local")
		}
		candidates = append(candidates, filepath.Join(localAppData, "agy", "bin"))
		candidates = append(candidates, filepath.Join(localAppData, "Programs", "antigravity", "resources", "bin"))
		candidates = append(candidates, filepath.Join(homeDir, ".gemini", "antigravity-cli", "bin"))
		candidates = append(candidates, filepath.Join(homeDir, ".gemini", "antigravity", "bin"))
	} else {
		candidates = append(candidates, filepath.Join(homeDir, ".gemini", "antigravity-cli", "bin"))
		candidates = append(candidates, filepath.Join(homeDir, ".gemini", "antigravity", "bin"))
		candidates = append(candidates, filepath.Join(homeDir, "Library", "Application Support", "agy", "bin"))
	}

	return candidates
}

// HijackCli injects wrapper scripts to route native 'agy' CLI traffic through proxy
func HijackCli(enable bool, appData, homeDir, caPath string, logCallback func(string)) {
	binDirs := getCliCandidates(appData, homeDir)
	exeName := "agy"
	realExeName := "agy_real"
	if runtime.GOOS == "windows" {
		exeName = "agy.exe"
		realExeName = "agy_real.exe"
	}

	proxyUrl := "http://127.0.0.1:18443"

	for _, dir := range binDirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		originalPath := filepath.Join(dir, exeName)
		renamedPath := filepath.Join(dir, realExeName)
		batWrapperPath := filepath.Join(dir, "agy.bat")
		shWrapperPath := filepath.Join(dir, "agy") // Shell wrapper on Unix / Git Bash

		if enable {
			realExeExists := false
			if _, err := os.Stat(renamedPath); err == nil {
				realExeExists = true
			}

			originalExeExists := false
			if _, err := os.Stat(originalPath); err == nil {
				originalExeExists = true
			}

			if !realExeExists && !originalExeExists {
				continue
			}

			if originalExeExists {
				stats, err := os.Lstat(originalPath)
				// If original exists and is a real binary (not wrapper script)
				if err == nil && stats.Mode().IsRegular() && stats.Size() > 1024*1024 {
					errRename := os.Rename(originalPath, renamedPath)
					if errRename == nil {
						logCallback(fmt.Sprintf("[CliHijacker] Renamed %s to %s in %s", exeName, realExeName, dir))
						realExeExists = true
					}
				}
			}

			if realExeExists {
				// 1. Write Windows Batch Wrapper
				combinedCa := getCombinedCaPath(caPath)
				batContent := fmt.Sprintf("@echo off\r\n"+
					"set HTTP_PROXY=%s\r\n"+
					"set HTTPS_PROXY=%s\r\n"+
					"set NO_PROXY=localhost,127.0.0.1\r\n"+
					"set SSL_CERT_FILE=%s\r\n"+
					"\"%%~dp0%s\" %%*\r\n", proxyUrl, proxyUrl, combinedCa, realExeName)

				_ = os.WriteFile(batWrapperPath, []byte(batContent), 0644)

				// 2. Write Unix Shell Wrapper (无需注入 SSL_CERT_FILE，回退由系统 Keychain 信任)
				shContent := fmt.Sprintf("#!/bin/bash\n"+
					"export HTTP_PROXY=%s\n"+
					"export HTTPS_PROXY=%s\n"+
					"export NO_PROXY=localhost,127.0.0.1\n"+
					"exec \"$(dirname \"$0\")/%s\" \"$@\"\n", proxyUrl, proxyUrl, realExeName)

				_ = os.WriteFile(shWrapperPath, []byte(shContent), 0755)

				logCallback(fmt.Sprintf("[CliHijacker] Successfully hijacked agy CLI in %s", dir))
			}
		} else {
			// Restore original CLI
			realExeExists := false
			if _, err := os.Stat(renamedPath); err == nil {
				realExeExists = true
			}

			_ = os.Remove(batWrapperPath)
			if runtime.GOOS == "windows" {
				_ = os.Remove(shWrapperPath)
			} else {
				if _, err := os.Stat(originalPath); err == nil {
					stats, errStats := os.Lstat(originalPath)
					if errStats == nil && stats.Size() < 1024*1024 {
						_ = os.Remove(originalPath)
					}
				}
			}

			if realExeExists {
				if _, err := os.Stat(originalPath); os.IsNotExist(err) {
					errRename := os.Rename(renamedPath, originalPath)
					if errRename == nil {
						logCallback(fmt.Sprintf("[CliHijacker] Restored %s to %s in %s", realExeName, exeName, dir))
					}
				} else {
					// Clean up backup if original already exists
					_ = os.Remove(renamedPath)
				}
			}
		}
	}
}

// UpdateAgentapiBat updates script wrappers to set/remove proxy env vars
func UpdateAgentapiBat(enable bool, appData, homeDir, caPath string) bool {
	batCandidates := []string{
		filepath.Join(appData, "antigravity", "bin", "agentapi.bat"),
		filepath.Join(appData, "Antigravity", "bin", "agentapi.bat"),
		filepath.Join(homeDir, ".antigravity", "bin", "agentapi.bat"),
		filepath.Join(homeDir, ".gemini", "antigravity", "bin", "agentapi.bat"),
		filepath.Join(homeDir, ".gemini", "antigravity-cli", "bin", "agentapi.bat"),
		filepath.Join(homeDir, ".gemini", "antigravity-ide", "bin", "agentapi.bat"),
	}

	shCandidates := []string{
		filepath.Join(appData, "antigravity", "bin", "agentapi"),
		filepath.Join(appData, "Antigravity", "bin", "agentapi"),
		filepath.Join(homeDir, ".antigravity", "bin", "agentapi"),
		filepath.Join(homeDir, ".gemini", "antigravity", "bin", "agentapi"),
		filepath.Join(homeDir, ".gemini", "antigravity-cli", "bin", "agentapi"),
		filepath.Join(homeDir, ".gemini", "antigravity-ide", "bin", "agentapi"),
	}

	proxyUrl := "http://127.0.0.1:18443"
	batMarker := ":: ANTIGRAVITY_PROXY_INJECT"
	shMarker := "# ANTIGRAVITY_PROXY_INJECT"

	patchBat := func(path string) bool {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return false
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}

		content := string(data)
		if enable {
			if strings.Contains(content, batMarker) {
				return true
			}
			combinedCa := getCombinedCaPath(caPath)
			inject := fmt.Sprintf("%s\r\nset HTTP_PROXY=%s\r\nset HTTPS_PROXY=%s\r\nset NO_PROXY=localhost,127.0.0.1\r\nset SSL_CERT_FILE=%s\r\n",
				batMarker, proxyUrl, proxyUrl, combinedCa)

			re := regexp.MustCompile(`(?i)^(@echo off\s*[\r\n]+)`)
			if re.MatchString(content) {
				content = re.ReplaceAllString(content, "${1}"+inject)
			} else {
				content = inject + content
			}
			_ = os.WriteFile(path, []byte(content), 0644)
		} else {
			if !strings.Contains(content, batMarker) {
				return true
			}
			re := regexp.MustCompile(regexp.QuoteMeta(batMarker) + `\r?\n(?:set [^\r\n]+\r?\n){1,6}`)
			content = re.ReplaceAllString(content, "")
			_ = os.WriteFile(path, []byte(content), 0644)
		}
		return true
	}

	patchSh := func(path string) bool {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return false
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}

		content := string(data)
		if enable {
			if strings.Contains(content, shMarker) {
				return true
			}
			inject := fmt.Sprintf("%s\nexport HTTP_PROXY=%s\nexport HTTPS_PROXY=%s\nexport NO_PROXY=localhost,127.0.0.1\n",
				shMarker, proxyUrl, proxyUrl)

			re := regexp.MustCompile(`^(#![^\n]+\n)`)
			if re.MatchString(content) {
				content = re.ReplaceAllString(content, "${1}"+inject)
			} else {
				content = inject + content
			}
			_ = os.WriteFile(path, []byte(content), 0755)
		} else {
			if !strings.Contains(content, shMarker) {
				return true
			}
			re := regexp.MustCompile(regexp.QuoteMeta(shMarker) + `\n(?:export [^\n]+\n){1,6}`)
			content = re.ReplaceAllString(content, "")
			_ = os.WriteFile(path, []byte(content), 0755)
		}
		return true
	}

	batPatched := false
	for _, p := range batCandidates {
		if patchBat(p) {
			batPatched = true
		}
	}
	for _, p := range shCandidates {
		patchSh(p)
	}

	return batPatched
}

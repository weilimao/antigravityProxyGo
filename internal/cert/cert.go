package cert

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
)

const certCommonName = "NodeMITMProxyCA"

// CheckCertStatus 检查证书是否已安装在系统受信任根证书链中
func CheckCertStatus() bool {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("certutil", "-user", "-store", "ROOT", certCommonName)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		var out bytes.Buffer
		cmd.Stdout = &out
		err := cmd.Run()
		return err == nil && strings.Contains(out.String(), certCommonName)
	} else if runtime.GOOS == "darwin" {
		cmd := exec.Command("security", "find-certificate", "-c", certCommonName)
		err := cmd.Run()
		return err == nil
	}
	return false
}

// InstallCert 安装受信任根证书
func InstallCert(caCertPath string) (bool, string) {
	if _, err := os.Stat(caCertPath); os.IsNotExist(err) {
		return false, fmt.Sprintf("证书文件 ca.pem 未找到：%s", caCertPath)
	}

	if runtime.GOOS == "windows" {
		cmd := exec.Command("certutil", "-user", "-addstore", "-f", "ROOT", caCertPath)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		var out bytes.Buffer
		cmd.Stderr = &out
		cmd.Stdout = &out
		err := cmd.Run()
		if err != nil {
			return false, fmt.Sprintf("证书安装失败：%s", out.String())
		}
		return CheckCertStatus(), ""
	} else if runtime.GOOS == "darwin" {
		// 尝试导入到 login keychain-db
		homeDir, _ := os.UserHomeDir()
		keychainPath := fmt.Sprintf("%s/Library/Keychains/login.keychain-db", homeDir)
		cmd := exec.Command("security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", keychainPath, caCertPath)
		var out bytes.Buffer
		cmd.Stderr = &out
		cmd.Stdout = &out
		err := cmd.Run()
		if err != nil {
			// 备用逻辑，不带 -k
			cmdFallback := exec.Command("security", "add-trusted-cert", "-d", "-r", "trustRoot", caCertPath)
			errFallback := cmdFallback.Run()
			if errFallback != nil {
				return false, fmt.Sprintf("证书安装失败（Darwin）：%s", out.String())
			}
		}
		return CheckCertStatus(), ""
	}

	return false, "仅 Windows 和 macOS 支持证书自动导入"
}

// UninstallCert 卸载 Root CA 证书
func UninstallCert() (bool, string) {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("certutil", "-user", "-delstore", "ROOT", certCommonName)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		var out bytes.Buffer
		cmd.Stderr = &out
		cmd.Stdout = &out
		err := cmd.Run()
		if err != nil {
			return false, fmt.Sprintf("证书卸载失败：%s", out.String())
		}
		return !CheckCertStatus(), ""
	} else if runtime.GOOS == "darwin" {
		homeDir, _ := os.UserHomeDir()
		keychainPath := fmt.Sprintf("%s/Library/Keychains/login.keychain-db", homeDir)
		cmd := exec.Command("security", "delete-certificate", "-c", certCommonName, keychainPath)
		err := cmd.Run()
		if err != nil {
			cmdFallback := exec.Command("security", "delete-certificate", "-c", certCommonName)
			errFallback := cmdFallback.Run()
			if errFallback != nil {
				return false, "证书删除失败"
			}
		}
		return !CheckCertStatus(), ""
	}

	return false, "仅 Windows 和 macOS 支持证书自动卸载"
}

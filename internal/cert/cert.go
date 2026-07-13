package cert

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const certCommonName = "NodeMITMProxyCA"

// CheckCertStatus 检查证书是否已安装在系统受信任根证书链中
func CheckCertStatus(caCertPath string) bool {
	if runtime.GOOS == "windows" {
		// 解析 caCertPath 证书的十六进制序列号以供精确比对
		var queryTarget = certCommonName
		if caCertPath != "" {
			if certBytes, err := os.ReadFile(caCertPath); err == nil {
				if block, _ := pem.Decode(certBytes); block != nil && block.Type == "CERTIFICATE" {
					if parsedCert, err := x509.ParseCertificate(block.Bytes); err == nil {
						// 转换为 16 进制小写字符串，与 certutil 的输出格式一致
						queryTarget = fmt.Sprintf("%x", parsedCert.SerialNumber)
					}
				}
			}
		}

		// 1. 检查当前用户证书库
		cmdUser := exec.Command("certutil", "-user", "-store", "ROOT", queryTarget)
		hideWindow(cmdUser)
		var outUser bytes.Buffer
		cmdUser.Stdout = &outUser
		errUser := cmdUser.Run()
		if errUser == nil && strings.Contains(outUser.String(), queryTarget) {
			return true
		}

		// 2. 检查本地计算机（全局系统级）证书库
		cmdMachine := exec.Command("certutil", "-store", "ROOT", queryTarget)
		hideWindow(cmdMachine)
		var outMachine bytes.Buffer
		cmdMachine.Stdout = &outMachine
		errMachine := cmdMachine.Run()
		return errMachine == nil && strings.Contains(outMachine.String(), queryTarget)
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
		// 1. 尝试以管理员权限通过 PowerShell 运行 certutil 安装至本地计算机证书库（系统级全局）
		// 使用单引号包裹路径防空格，如果路径含有单引号则进行双写转义
		escapedPath := strings.ReplaceAll(caCertPath, "'", "''")
		psCmd := fmt.Sprintf("Start-Process certutil -ArgumentList '-addstore', '-f', 'ROOT', '%s' -Verb RunAs -Wait -WindowStyle Hidden", escapedPath)
		cmdElevated := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd)
		hideWindow(cmdElevated)
		_ = cmdElevated.Run()

		// 检查全局证书是否安装成功
		if CheckCertStatus(caCertPath) {
			return true, ""
		}

		// 2. 降级逻辑（UAC 被取消或执行失败）：安装至当前用户证书库
		cmdUser := exec.Command("certutil", "-user", "-addstore", "-f", "ROOT", caCertPath)
		hideWindow(cmdUser)
		var outUser bytes.Buffer
		cmdUser.Stderr = &outUser
		cmdUser.Stdout = &outUser
		errUser := cmdUser.Run()
		if errUser != nil {
			return false, fmt.Sprintf("证书安装失败（本地计算机与当前用户库均未成功）。用户库错误：%s", outUser.String())
		}
		return CheckCertStatus(caCertPath), ""
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
		return CheckCertStatus(caCertPath), ""
	}

	return false, "仅 Windows 和 macOS 支持证书自动导入"
}

// UninstallCert 卸载 Root CA 证书
func UninstallCert() (bool, string) {
	if runtime.GOOS == "windows" {
		// 1. 从当前用户证书库中卸载
		cmdUser := exec.Command("certutil", "-user", "-delstore", "ROOT", certCommonName)
		hideWindow(cmdUser)
		_ = cmdUser.Run()

		// 2. 尝试从本地计算机证书库中卸载（如果之前有残留）
		psCmd := fmt.Sprintf("Start-Process certutil -ArgumentList '-delstore', 'ROOT', '%s' -Verb RunAs -Wait -WindowStyle Hidden", certCommonName)
		cmdElevated := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd)
		hideWindow(cmdElevated)
		_ = cmdElevated.Run()

		return !CheckCertStatus(""), ""
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
		return !CheckCertStatus(""), ""
	}

	return false, "仅 Windows 和 macOS 支持证书自动卸载"
}

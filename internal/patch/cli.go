package patch

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

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
				batContent := fmt.Sprintf("@echo off\r\n"+
					"set HTTP_PROXY=%s\r\n"+
					"set HTTPS_PROXY=%s\r\n"+
					"set NO_PROXY=localhost,127.0.0.1,www.googleapis.com,accounts.google.com,oauth2.googleapis.com\r\n"+
					"set NODE_EXTRA_CA_CERTS=%s\r\n"+
					"\"%%~dp0%s\" %%*\r\n", proxyUrl, proxyUrl, caPath, realExeName)

				_ = os.WriteFile(batWrapperPath, []byte(batContent), 0644)

				// 2. Write Unix Shell Wrapper
				shContent := fmt.Sprintf("#!/bin/bash\n"+
					"export HTTP_PROXY=%s\n"+
					"export HTTPS_PROXY=%s\n"+
					"export NO_PROXY=localhost,127.0.0.1,www.googleapis.com,accounts.google.com,oauth2.googleapis.com\n"+
					"export NODE_EXTRA_CA_CERTS=%s\n"+
					"exec \"$(dirname \"$0\")/%s\" \"$@\"\n", proxyUrl, proxyUrl, caPath, realExeName)

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
			inject := fmt.Sprintf("%s\r\nset HTTP_PROXY=%s\r\nset HTTPS_PROXY=%s\r\nset NO_PROXY=localhost,127.0.0.1\r\n",
				batMarker, proxyUrl, proxyUrl)

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

package patch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ASAR Format Header Structure:
// Offset 0-3:   uint32 = 4
// Offset 4-7:   uint32 = headerSize (JSON size + padding + 8)
// Offset 8-11:  uint32 = headerSize - 4
// Offset 12-15: uint32 = jsonSize
// Offset 16..:  JSON Header
// After JSON:   Padding to 4-byte boundary
// After Padding: Files payload

type AsarFile struct {
	Path           string
	Offset         int64
	Size           int64
	EntryMap       map[string]interface{}
	OriginalOffset int64
}

func readUint32(b []byte, offset int) uint32 {
	return uint32(b[offset]) | uint32(b[offset+1])<<8 | uint32(b[offset+2])<<16 | uint32(b[offset+3])<<24
}

func writeUint32(b []byte, offset int, val uint32) {
	b[offset] = byte(val)
	b[offset+1] = byte(val >> 8)
	b[offset+2] = byte(val >> 16)
	b[offset+3] = byte(val >> 24)
}

func walkAsarTree(tree map[string]interface{}, currentPath string, files *[]AsarFile) {
	for name, val := range tree {
		entry, ok := val.(map[string]interface{})
		if !ok {
			continue
		}

		nextPath := name
		if currentPath != "" {
			nextPath = currentPath + "/" + name
		}

		if subFiles, exists := entry["files"].(map[string]interface{}); exists {
			walkAsarTree(subFiles, nextPath, files)
		} else if offsetStr, exists := entry["offset"].(string); exists {
			offset, _ := strconv.ParseInt(offsetStr, 10, 64)
			size := int64(0)
			if sizeVal, exists := entry["size"]; exists {
				switch s := sizeVal.(type) {
				case float64:
					size = int64(s)
				case int64:
					size = s
				case int:
					size = int64(s)
				}
			}

			*files = append(*files, AsarFile{
				Path:           nextPath,
				Offset:         offset,
				Size:           size,
				EntryMap:       entry,
				OriginalOffset: offset,
			})
		}
	}
}

// PatchAsar reads an Electron app.asar file, injects proxy configuration into
// dist/languageServer.js, and saves it. If a backup does not exist, it creates one.
func PatchAsar(asarPath, caCertPath string, logCallback func(string)) error {
	if _, err := os.Stat(asarPath); os.IsNotExist(err) {
		return fmt.Errorf("app.asar 未找到: %s", asarPath)
	}

	bakPath := asarPath + ".bak"
	if _, err := os.Stat(bakPath); os.IsNotExist(err) {
		// Create backup
		if err := copyFile(asarPath, bakPath); err != nil {
			return fmt.Errorf("创建 app.asar 备份失败: %v", err)
		}
		logCallback("💾 Created backup of original app.asar.")
	} else {
		// Restore from backup to work on a clean original
		if err := copyFile(bakPath, asarPath); err != nil {
			return fmt.Errorf("从备份恢复 app.asar 失败: %v", err)
		}
		logCallback("⏪ Restored original app.asar from backup before patching.")
	}

	asarData, err := os.ReadFile(asarPath)
	if err != nil {
		return err
	}

	if len(asarData) < 16 {
		return errors.New("无效的 app.asar 文件（大小不足 16 字节）")
	}

	magic := readUint32(asarData, 0)
	headerSize := readUint32(asarData, 4)
	size2 := readUint32(asarData, 8)
	jsonSize := readUint32(asarData, 12)

	if magic != 4 || size2 != headerSize-4 {
		return errors.New("无效的 ASAR 文件格式（Pickle 头部校验失败）")
	}

	if int(16+jsonSize) > len(asarData) {
		return errors.New("无效的 ASAR 文件格式（JSON 头部长度超出文件边界）")
	}

	jsonBytes := asarData[16 : 16+jsonSize]
	var root map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &root); err != nil {
		return fmt.Errorf("解析 ASAR 头部 JSON 失败: %v", err)
	}

	filesMap, ok := root["files"].(map[string]interface{})
	if !ok {
		return errors.New("无效的 ASAR 头部，未找到 files 根目录")
	}

	var files []AsarFile
	walkAsarTree(filesMap, "", &files)

	// Locate dist/languageServer.js
	var targetFile *AsarFile
	for i := range files {
		if files[i].Path == "dist/languageServer.js" {
			targetFile = &files[i]
			break
		}
	}

	if targetFile == nil {
		return errors.New("未在 app.asar 中找到 dist/languageServer.js")
	}

	payloadOffset := int64(8 + headerSize)
	fileStart := payloadOffset + targetFile.Offset
	fileEnd := fileStart + targetFile.Size

	if fileEnd > int64(len(asarData)) {
		return errors.New("dist/languageServer.js 数据超出 ASAR 文件边界")
	}

	originalJs := string(asarData[fileStart:fileEnd])
	if strings.Contains(originalJs, "env['HTTP_PROXY'] = 'http://127.0.0.1:18443'") {
		logCallback("ℹ️ languageServer.js already contains patch, skipping write.")
		return nil
	}

	// Perform patching
	// Find setupNodeWrapper call: (0, setupNodeWrapper)(env); or similar
	idx := strings.Index(originalJs, "setupNodeWrapper)(env)")
	if idx == -1 {
		idx = strings.Index(originalJs, "setupNodeWrapper)(process.env)")
	}

	if idx == -1 {
		return errors.New("未能在 languageServer.js 中找到 setupNodeWrapper(env) 注入点")
	}

	// Find the end of statement (e.g. semicolon)
	stmtEnd := idx
	for stmtEnd < len(originalJs) && originalJs[stmtEnd] != ';' {
		stmtEnd++
	}
	if stmtEnd < len(originalJs) {
		stmtEnd++ // Include semicolon
	}

	// We extract the exact wrapper call statement
	// Usually: (0, setupNodeWrapper)(env); or wrapper(env);
	stmtStart := idx
	for stmtStart > 0 && originalJs[stmtStart] != '\n' && originalJs[stmtStart] != ';' && originalJs[stmtStart] != '{' {
		stmtStart--
	}
	if stmtStart > 0 {
		stmtStart++
	}

	wrapperCall := strings.TrimSpace(originalJs[stmtStart:stmtEnd])

	injectStr := fmt.Sprintf(`%s
        // INJECTED BY ANTIGRAVITY PROXY DESKTOP
        env['HTTP_PROXY']  = 'http://127.0.0.1:18443';
        env['HTTPS_PROXY'] = 'http://127.0.0.1:18443';
        env['http_proxy']  = 'http://127.0.0.1:18443';
        env['https_proxy'] = 'http://127.0.0.1:18443';
        env['NO_PROXY']    = 'localhost,127.0.0.1';
        env['no_proxy']    = 'localhost,127.0.0.1';
        try {
            const os = require('os');
            const path = require('path');
            const fs = require('fs');
            const defaultUserData = process.platform === 'win32'
                ? path.join(os.homedir(), 'AppData', 'Roaming', 'antigravity-proxy-desktop')
                : path.join(os.homedir(), 'Library', 'Application Support', 'antigravity-proxy-desktop');
            let caPath = '%s';
            try {
                const configPath = path.join(defaultUserData, 'config.json');
                if (fs.existsSync(configPath)) {
                    const config = JSON.parse(fs.readFileSync(configPath, 'utf8'));
                    if (config.dataDirectory) {
                        caPath = path.join(config.dataDirectory, 'certs', 'certs', 'ca.pem');
                    }
                }
            } catch (err) {}
            env['SSL_CERT_FILE'] = caPath;
        } catch (e) {}`, wrapperCall, filepath.ToSlash(caCertPath))

	newJs := originalJs[:stmtStart] + injectStr + originalJs[stmtEnd:]
	newJsBytes := []byte(newJs)
	newSize := int64(len(newJsBytes))
	diffSize := newSize - targetFile.Size

	logCallback("📝 Injected proxy env vars into languageServer.js.")

	// Update offsets of all succeeding files in JSON metadata
	targetFile.EntryMap["size"] = newSize
	targetFile.Size = newSize

	for i := range files {
		if files[i].Offset > targetFile.Offset {
			files[i].Offset += diffSize
			files[i].EntryMap["offset"] = strconv.FormatInt(files[i].Offset, 10)
		}
	}

	// Re-serialize JSON Header
	newJsonBytes, err := json.Marshal(root)
	if err != nil {
		return fmt.Errorf("序列化新 ASAR 头部失败: %v", err)
	}

	newJsonSize := uint32(len(newJsonBytes))
	paddingSize := uint32(0)
	if newJsonSize%4 != 0 {
		paddingSize = 4 - (newJsonSize % 4)
	}

	newHeaderSize := newJsonSize + paddingSize + 8

	// Construct New ASAR File
	var outBuf bytes.Buffer
	outBuf.Grow(len(asarData) + int(diffSize) + 64)

	// Write Pickle header fields
	headerBytes := make([]byte, 16)
	writeUint32(headerBytes, 0, 4)
	writeUint32(headerBytes, 4, newHeaderSize)
	writeUint32(headerBytes, 8, newHeaderSize-4)
	writeUint32(headerBytes, 12, newJsonSize)
	outBuf.Write(headerBytes)

	// Write JSON string
	outBuf.Write(newJsonBytes)

	// Write padding
	if paddingSize > 0 {
		outBuf.Write(make([]byte, paddingSize))
	}

	// Write Files Payload
	// Sort files by original offset to write sequentially
	sort.Slice(files, func(i, j int) bool {
		return files[i].Offset < files[j].Offset
	})

	for _, f := range files {
		if f.Path == "dist/languageServer.js" {
			outBuf.Write(newJsBytes)
		} else {
			fStart := payloadOffset + f.OriginalOffset
			fEnd := fStart + f.Size
			outBuf.Write(asarData[fStart:fEnd])
		}
	}

	err = os.WriteFile(asarPath, outBuf.Bytes(), 0644)
	if err == nil {
		logCallback("📦 Repacked app.asar successfully.")
	}
	return err
}

// RestoreAsar restores the app.asar file from app.asar.bak if it exists.
func RestoreAsar(asarPath string) error {
	bakPath := asarPath + ".bak"
	if _, err := os.Stat(bakPath); err == nil {
		if err := copyFile(bakPath, asarPath); err != nil {
			return err
		}
		_ = os.Remove(bakPath)
		fmt.Println("[ASAR Patcher] Restored app.asar from backup and removed bak.")
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

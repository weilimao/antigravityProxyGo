package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"antigravity-proxy/internal/netutil"
)

type ReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type GitHubRelease struct {
	TagName string         `json:"tag_name"`
	Body    string         `json:"body"`
	HTMLURL string         `json:"html_url"`
	Assets  []ReleaseAsset `json:"assets"`
}

type ProgressCallback func(percent int, downloaded, total int64)

type Manager struct {
	sync.Mutex
	currentVersion string
	tempDir        string
	owner          string
	repo           string
	isDownloading  bool
}

func NewManager(version, tempDir string) *Manager {
	return &Manager{
		currentVersion: strings.TrimPrefix(version, "v"),
		tempDir:        tempDir,
		owner:          "weilimao",
		repo:           "antigravityProxyGo",
	}
}

func cleanVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

func isNewerVersion(current, latest string) bool {
	cParts := strings.Split(cleanVersion(current), ".")
	lParts := strings.Split(cleanVersion(latest), ".")

	parse := func(parts []string) []int {
		res := make([]int, 3)
		for i := 0; i < len(parts) && i < 3; i++ {
			val, _ := strconv.Atoi(parts[i])
			res[i] = val
		}
		return res
	}

	c := parse(cParts)
	l := parse(lParts)

	if l[0] > c[0] {
		return true
	}
	if l[0] < c[0] {
		return false
	}
	if l[1] > c[1] {
		return true
	}
	if l[1] < c[1] {
		return false
	}
	return l[2] > c[2]
}

func (m *Manager) CheckForUpdates() (bool, *GitHubRelease, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", m.owner, m.repo), nil)
	if err != nil {
		return false, nil, err
	}
	req.Header.Set("User-Agent", "AntigravityProxy-Updater")

	client := netutil.NewClient(8 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return false, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return false, nil, fmt.Errorf("GitHub API returned status code %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, nil, err
	}

	var release GitHubRelease
	if err := json.Unmarshal(bodyBytes, &release); err != nil {
		return false, nil, err
	}

	hasUpdate := isNewerVersion(m.currentVersion, release.TagName)
	return hasUpdate, &release, nil
}

func (m *Manager) findPlatformAsset(assets []ReleaseAsset) *ReleaseAsset {
	if len(assets) == 0 {
		return nil
	}

	if runtime.GOOS == "windows" {
		for _, asset := range assets {
			nameLower := strings.ToLower(asset.Name)
			if strings.HasSuffix(nameLower, ".exe") && !strings.HasSuffix(nameLower, ".blockmap") {
				return &asset
			}
		}
	} else if runtime.GOOS == "darwin" {
		// macOS: match arch, fallback to general .dmg, then .zip
		arch := runtime.GOARCH
		var dmg, fallbackDmg, zip, fallbackZip *ReleaseAsset

		for _, asset := range assets {
			nameLower := strings.ToLower(asset.Name)
			if strings.HasSuffix(nameLower, ".dmg") {
				if strings.Contains(nameLower, arch) {
					dmg = &asset
				} else {
					fallbackDmg = &asset
				}
			} else if strings.HasSuffix(nameLower, ".zip") {
				if strings.Contains(nameLower, arch) {
					zip = &asset
				} else {
					fallbackZip = &asset
				}
			}
		}

		if dmg != nil {
			return dmg
		}
		if fallbackDmg != nil {
			return fallbackDmg
		}
		if zip != nil {
			return zip
		}
		if fallbackZip != nil {
			return fallbackZip
		}
	}
	return nil
}

func (m *Manager) DownloadUpdate(assets []ReleaseAsset, progress ProgressCallback) (string, error) {
	m.Lock()
	if m.isDownloading {
		m.Unlock()
		return "", errors.New("下载已经在进行中")
	}
	m.isDownloading = true
	m.Unlock()

	defer func() {
		m.Lock()
		m.isDownloading = false
		m.Unlock()
	}()

	asset := m.findPlatformAsset(assets)
	if asset == nil {
		return "", errors.New("未找到适用于当前系统平台的安装包资源")
	}

	destPath := filepath.Join(m.tempDir, asset.Name)
	_ = os.MkdirAll(m.tempDir, 0755)

	// Download file
	req, err := http.NewRequest("GET", asset.BrowserDownloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "AntigravityProxy-Updater-Downloader")

	client := netutil.NewClient(30 * time.Minute)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("下载失败，HTTP 状态码: %d", resp.StatusCode)
	}

	totalBytes := resp.ContentLength
	if totalBytes <= 0 {
		totalBytes = asset.Size
	}

	out, err := os.OpenFile(destPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return "", err
	}
	defer out.Close()

	buffer := make([]byte, 32*1024)
	var downloaded int64 = 0

	for {
		nr, er := resp.Body.Read(buffer)
		if nr > 0 {
			nw, ew := out.Write(buffer[0:nr])
			if nw > 0 {
				downloaded += int64(nw)
			}
			if ew != nil {
				return "", ew
			}
			if nr != nw {
				return "", io.ErrShortWrite
			}

			if totalBytes > 0 && progress != nil {
				percent := int(math.Round(float64(downloaded) / float64(totalBytes) * 100.0))
				progress(percent, downloaded, totalBytes)
			}
		}
		if er != nil {
			if er == io.EOF {
				break
			}
			return "", er
		}
	}

	return destPath, nil
}

func (m *Manager) InstallUpdate(filePath string) error {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("未找到安装包文件: %s", filePath)
	}

	err := installUpdateOS(filePath)
	if err != nil {
		return err
	}

	// Exit host application
	go func() {
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()
	return nil
}

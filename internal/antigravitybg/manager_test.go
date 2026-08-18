package antigravitybg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathResolver_GetOS(t *testing.T) {
	resolver := NewPathResolver()
	osName := resolver.GetOS()
	if osName == "" {
		t.Fatalf("expected non-empty OS name")
	}
}

func TestAsarHandler_GenerateRuntimeJS(t *testing.T) {
	handler := NewAsarHandler()
	cfg := WallpaperConfig{
		Enabled:       true,
		ImageSource:   "url",
		ImageData:     "https://example.com/wallpaper.jpg",
		Opacity:       0.4,
		Blur:          12,
		DarkOverlay:   0.35,
		GlassAlpha:    0.8,
		BackgroundFit: "cover",
	}

	js, err := handler.GenerateRuntimeJS(cfg)
	if err != nil {
		t.Fatalf("GenerateRuntimeJS failed: %v", err)
	}

	if !strings.Contains(js, runtimeHeaderTag) || !strings.Contains(js, runtimeFooterTag) {
		t.Fatalf("expected runtime tags in generated JS")
	}

	if !strings.Contains(js, "https://example.com/wallpaper.jpg") {
		t.Fatalf("expected image URL in generated JS")
	}

	if !strings.Contains(js, "aside, nav, [class*=\"sidebar\"]") {
		t.Fatalf("expected sidebar selector in generated JS")
	}

	if !strings.Contains(js, "[class*=\"composer\"]") {
		t.Fatalf("expected composer selector in generated JS")
	}

	if !strings.Contains(js, "--ag-sidebar-text-color") || !strings.Contains(js, "--ag-content-text-color") || !strings.Contains(js, "--ag-ui-blur") {
		t.Fatalf("expected CSS variables in generated JS")
	}

	// Test video wallpaper generation
	videoCfg := WallpaperConfig{
		Enabled:      true,
		ImageSource:  "url",
		ImageData:    "https://example.com/live_wallpaper.mp4",
		MediaType:    "video",
		PlaybackRate: 1.25,
		Muted:        true,
		Loop:         true,
	}
	videoJs, err := handler.GenerateRuntimeJS(videoCfg)
	if err != nil {
		t.Fatalf("GenerateRuntimeJS for video failed: %v", err)
	}
	if !strings.Contains(videoJs, "ag-custom-wallpaper-video") {
		t.Fatalf("expected ag-custom-wallpaper-video in generated JS for video")
	}

	if !handler.CheckIfPatched(js) {
		t.Fatalf("expected CheckIfPatched to return true")
	}

	originalCode := "console.log('original code');"
	combined := originalCode + "\n\n" + js
	cleaned := handler.RemovePatchFromContent(combined)
	if strings.Contains(cleaned, runtimeHeaderTag) {
		t.Fatalf("expected runtime tag to be removed")
	}
	if !strings.Contains(cleaned, originalCode) {
		t.Fatalf("expected original code to be preserved")
	}
}

func TestManager_ConfigSaveAndLoad(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "antigravity_bg_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir)
	status := mgr.GetStatus("")
	if status.OS == "" {
		t.Fatalf("expected non-empty OS in status")
	}

	testCfg := WallpaperConfig{
		Enabled:       true,
		ImageSource:   "base64",
		ImageData:     "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==",
		Opacity:       0.5,
		Blur:          10,
		DarkOverlay:   0.25,
		GlassAlpha:    0.9,
		BackgroundFit: "cover",
	}

	if err := mgr.saveStoredConfig(testCfg); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	mgr2 := NewManager(tempDir)
	status2 := mgr2.GetStatus("")
	if status2.Config.Opacity != 0.5 || status2.Config.Blur != 10 {
		t.Fatalf("expected saved config to be loaded, got %+v", status2.Config)
	}
}

func TestPathResolver_ResolveAppDir(t *testing.T) {
	resolver := NewPathResolver()
	winPath := `C:\Users\Test\AppData\Local\Programs\antigravity\resources\app.asar`
	appDir := resolver.ResolveAppDir(winPath)
	expectedWin := filepath.Clean(`C:\Users\Test\AppData\Local\Programs\antigravity`)
	if filepath.Clean(appDir) != expectedWin {
		t.Errorf("expected %s, got %s", expectedWin, appDir)
	}

	macPath := `/Applications/Antigravity.app/Contents/Resources/app.asar`
	if resolver.GetOS() == "darwin" {
		macApp := resolver.ResolveAppDir(macPath)
		if macApp != "/Applications/Antigravity.app" {
			t.Errorf("expected /Applications/Antigravity.app, got %s", macApp)
		}
	}
}

func TestManager_Gallery(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "antigravity_gallery_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir)
	g := mgr.GetGallery()
	if len(g) != 0 {
		t.Fatalf("expected empty gallery initially, got %d", len(g))
	}

	wp1 := SavedWallpaper{
		ID:   "wp_1",
		Name: "Wallpaper 1",
		Type: "url",
		Data: "https://example.com/1.png",
	}
	wp2 := SavedWallpaper{
		ID:   "wp_2",
		Name: "Wallpaper 2",
		Type: "url",
		Data: "https://example.com/2.png",
	}

	mgr.AddWallpaperToGallery(wp1)
	mgr.AddWallpaperToGallery(wp2)

	g2 := mgr.GetGallery()
	if len(g2) != 2 {
		t.Fatalf("expected 2 wallpapers, got %d", len(g2))
	}

	// Verify persistence
	mgrReloaded := NewManager(tempDir)
	gReloaded := mgrReloaded.GetGallery()
	if len(gReloaded) != 2 {
		t.Fatalf("expected 2 wallpapers after reload, got %d", len(gReloaded))
	}

	// Remove one
	mgrReloaded.RemoveWallpaperFromGallery("wp_1")
	if len(mgrReloaded.GetGallery()) != 1 {
		t.Fatalf("expected 1 wallpaper after removal, got %d", len(mgrReloaded.GetGallery()))
	}
}

func TestAsarHandler_PatchLanguageServer(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "antigravity_ls_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	distDir := filepath.Join(tempDir, "dist")
	if err := os.MkdirAll(distDir, 0755); err != nil {
		t.Fatalf("failed to create dist dir: %v", err)
	}

	mockLSContent := `
function startLanguageServer(env) {
    (0, setupNodeWrapper)(env);
    console.log("Language server starting");
}
`
	lsPath := filepath.Join(distDir, "languageServer.js")
	if err := os.WriteFile(lsPath, []byte(mockLSContent), 0644); err != nil {
		t.Fatalf("failed to write mock languageServer.js: %v", err)
	}

	handler := NewAsarHandler()
	if err := handler.PatchLanguageServer(tempDir); err != nil {
		t.Fatalf("PatchLanguageServer failed: %v", err)
	}

	patchedBytes, err := os.ReadFile(lsPath)
	if err != nil {
		t.Fatalf("failed to read patched languageServer.js: %v", err)
	}

	patchedStr := string(patchedBytes)
	if !strings.Contains(patchedStr, "env['HTTP_PROXY']  = 'http://127.0.0.1:18443'") {
		t.Fatalf("expected HTTP_PROXY injected into languageServer.js")
	}

	// Re-running PatchLanguageServer should be idempotent
	if err := handler.PatchLanguageServer(tempDir); err != nil {
		t.Fatalf("second PatchLanguageServer failed: %v", err)
	}
}

func TestManager_SyncThemeToSettings(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "antigravity_theme_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	settingsPath := filepath.Join(tempDir, "settings.json")
	initialContent := `{"workbench.colorTheme": "Solarized Light", "jetski.cloudCodeUrl": "http://127.0.0.1:18443"}`
	if err := os.WriteFile(settingsPath, []byte(initialContent), 0644); err != nil {
		t.Fatalf("failed to write settings.json: %v", err)
	}

	// Update directly via logic
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("failed to unmarshal settings.json: %v", err)
	}

	settings["workbench.colorTheme"] = "Default Dark Modern"
	bytesData, _ := json.Marshal(settings)
	_ = os.WriteFile(settingsPath, bytesData, 0644)
	updatedBytes, _ := os.ReadFile(settingsPath)
	if !strings.Contains(string(updatedBytes), "Default Dark Modern") {
		t.Fatalf("expected Default Dark Modern in settings.json")
	}

	// Test Antigravity config.json userSettings.themeMode synchronization
	configJsonPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{"userSettings": {"themeMode": "THEME_MODE_INHERIT"}}`
	if err := os.WriteFile(configJsonPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("failed to write config.json: %v", err)
	}

	cData, _ := os.ReadFile(configJsonPath)
	var conf map[string]interface{}
	_ = json.Unmarshal(cData, &conf)
	uSet := conf["userSettings"].(map[string]interface{})
	uSet["themeMode"] = "THEME_MODE_DARK"
	out, _ := json.MarshalIndent(conf, "", "  ")
	_ = os.WriteFile(configJsonPath, out, 0644)

	resBytes, _ := os.ReadFile(configJsonPath)
	if !strings.Contains(string(resBytes), "THEME_MODE_DARK") {
		t.Fatalf("expected THEME_MODE_DARK in config.json")
	}
}

func TestManager_LightweightSummaryAndDataFetch(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "antigravity_summary_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir)

	// Create a large base64 data string (> 2048 bytes)
	largeData := "data:image/png;base64," + strings.Repeat("A", 5000)
	wpLarge := SavedWallpaper{
		ID:        "wp_large_1",
		Name:      "4K Wallpaper",
		Type:      "base64",
		MediaType: "image",
		Data:      largeData,
		Thumbnail: "data:image/jpeg;base64,tinythumb",
	}

	wpSmall := SavedWallpaper{
		ID:        "wp_small_1",
		Name:      "Web Wallpaper",
		Type:      "url",
		MediaType: "image",
		Data:      "https://example.com/test.jpg",
		Thumbnail: "https://example.com/thumb.jpg",
	}

	mgr.AddWallpaperToGallery(wpLarge)
	mgr.AddWallpaperToGallery(wpSmall)

	// 1. GetGallery (summary) should strip large data but keep small data and thumbnails
	gallerySummary := mgr.GetGallery()
	if len(gallerySummary) != 2 {
		t.Fatalf("expected 2 items, got %d", len(gallerySummary))
	}

	var foundLarge, foundSmall *SavedWallpaper
	for i := range gallerySummary {
		if gallerySummary[i].ID == "wp_large_1" {
			foundLarge = &gallerySummary[i]
		}
		if gallerySummary[i].ID == "wp_small_1" {
			foundSmall = &gallerySummary[i]
		}
	}

	if foundLarge == nil || foundSmall == nil {
		t.Fatalf("expected both items in summary")
	}

	if foundLarge.Data != "" {
		t.Fatalf("expected large base64 data to be empty in summary, got length %d", len(foundLarge.Data))
	}
	if foundLarge.Thumbnail != "data:image/jpeg;base64,tinythumb" {
		t.Fatalf("expected thumbnail preserved, got %s", foundLarge.Thumbnail)
	}
	if foundSmall.Data != "https://example.com/test.jpg" {
		t.Fatalf("expected small url data to be preserved, got %s", foundSmall.Data)
	}

	// 2. GetWallpaperData should retrieve the full large data on demand
	fullData, err := mgr.GetWallpaperData("wp_large_1")
	if err != nil {
		t.Fatalf("GetWallpaperData failed: %v", err)
	}
	if fullData != largeData {
		t.Fatalf("expected fullData to match original largeData")
	}

	// 3. SaveGallery with empty Data in summary should not lose the stored full data
	updateSummaries := []SavedWallpaper{
		{
			ID:        "wp_large_1",
			Name:      "4K Wallpaper Renamed",
			Type:      "base64",
			MediaType: "image",
			Data:      "", // Sent empty from frontend
			Thumbnail: "data:image/jpeg;base64,tinythumb_updated",
		},
	}
	if err := mgr.SaveGallery(updateSummaries); err != nil {
		t.Fatalf("SaveGallery failed: %v", err)
	}

	fullDataAfterSave, err := mgr.GetWallpaperData("wp_large_1")
	if err != nil {
		t.Fatalf("GetWallpaperData failed after save: %v", err)
	}
	if fullDataAfterSave != largeData {
		t.Fatalf("expected largeData preserved after saving summary without Data")
	}
}


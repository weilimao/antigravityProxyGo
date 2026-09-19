package account

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateOpenCodeAccountInput(t *testing.T) {
	tests := []struct {
		name    string
		input   OpenCodeAccountInput
		wantErr bool
	}{
		{
			name: "valid input",
			input: OpenCodeAccountInput{
				BaseURL:     "https://opencode.ai/zen/v1",
				AccessToken: "sk-test-123456",
			},
			wantErr: false,
		},
		{
			name: "empty baseUrl defaults to official",
			input: OpenCodeAccountInput{
				BaseURL:     "",
				AccessToken: "sk-test-123456",
			},
			wantErr: false,
		},
		{
			name: "missing access token",
			input: OpenCodeAccountInput{
				BaseURL:     "https://opencode.ai/zen/v1",
				AccessToken: "",
			},
			wantErr: true,
		},
		{
			name: "invalid base url scheme",
			input: OpenCodeAccountInput{
				BaseURL:     "ftp://invalid.url",
				AccessToken: "sk-test-123456",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOpenCodeAccountInput(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateOpenCodeAccountInput() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewOpenCodeAccount(t *testing.T) {
	in := OpenCodeAccountInput{
		BaseURL:      "https://opencode.ai/zen/v1",
		AccessToken:  "sk-pKT7h7zi2R8Mpy5OQBFlyRe6SMoZwGUj4zgycVcSue3VxoFQNaAIlo4UfCRvhqJ0",
		Label:        "MyOpenCode",
		DefaultModel: "gpt-5-nano",
	}
	acc := NewOpenCodeAccount(in)
	if acc.Email != "MyOpenCode" {
		t.Errorf("expected Email 'MyOpenCode', got '%s'", acc.Email)
	}
	if acc.Provider != "opencode" {
		t.Errorf("expected Provider 'opencode', got '%s'", acc.Provider)
	}
	if acc.ScopeType != "opencode" {
		t.Errorf("expected ScopeType 'opencode', got '%s'", acc.ScopeType)
	}
	if acc.AccessToken != in.AccessToken {
		t.Errorf("expected AccessToken '%s', got '%s'", in.AccessToken, acc.AccessToken)
	}
	if acc.DefaultModel != "gpt-5-nano" {
		t.Errorf("expected DefaultModel 'gpt-5-nano', got '%s'", acc.DefaultModel)
	}
	if !acc.Enabled {
		t.Error("expected Enabled true")
	}

	// Auto label when empty
	in2 := OpenCodeAccountInput{
		AccessToken: "sk-abcdef123456",
	}
	acc2 := NewOpenCodeAccount(in2)
	if acc2.Email != "OpenCode-123456" {
		t.Errorf("expected auto label 'OpenCode-123456', got '%s'", acc2.Email)
	}
}

func TestParseOpenCodeAuthInfo(t *testing.T) {
	jsonContent := []byte(`{
		"opencode": {
			"type": "api",
			"key": "sk-test-from-auth-json"
		}
	}`)
	parsed, err := ParseOpenCodeAuthInfo(jsonContent)
	if err != nil {
		t.Fatalf("ParseOpenCodeAuthInfo failed: %v", err)
	}
	if parsed.AccessToken != "sk-test-from-auth-json" {
		t.Errorf("expected token 'sk-test-from-auth-json', got '%s'", parsed.AccessToken)
	}
	if parsed.BaseURL != DefaultOpenCodeBaseURL {
		t.Errorf("expected BaseURL '%s', got '%s'", DefaultOpenCodeBaseURL, parsed.BaseURL)
	}

	// Invalid json
	_, err = ParseOpenCodeAuthInfo([]byte(`invalid`))
	if err == nil {
		t.Error("expected error for invalid json")
	}

	// Missing key
	_, err = ParseOpenCodeAuthInfo([]byte(`{}`))
	if err == nil {
		t.Error("expected error for missing key")
	}
}

func TestOpenCodeAccountManagerCRUD(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "opencode_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(tmpDir)
	})

	m := NewManager()
	m.Init(tmpDir)

	// Add
	id, err := m.AddOpenCodeAccount(OpenCodeAccountInput{
		AccessToken:  "sk-first-key",
		Label:        "First",
		DefaultModel: "claude-sonnet-4-6",
	})
	if err != nil {
		t.Fatalf("AddOpenCodeAccount failed: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty id")
	}

	acc := m.GetAccountByID(id)
	if acc == nil {
		t.Fatal("GetAccountByID returned nil")
	}
	if acc.Email != "First" {
		t.Errorf("expected Email 'First', got '%s'", acc.Email)
	}
	if !IsOpenCodeAvailable(acc) {
		t.Error("expected account to be available")
	}

	// Update
	updated, err := m.UpdateOpenCodeAccount(id, OpenCodeAccountInput{
		AccessToken:  "sk-updated-key",
		Label:        "First-Renamed",
		DefaultModel: "gpt-5",
	})
	if err != nil {
		t.Fatalf("UpdateOpenCodeAccount failed: %v", err)
	}
	if updated.Email != "First-Renamed" {
		t.Errorf("expected 'First-Renamed', got '%s'", updated.Email)
	}
	if updated.AccessToken != "sk-updated-key" {
		t.Errorf("expected 'sk-updated-key', got '%s'", updated.AccessToken)
	}
	if updated.DefaultModel != "gpt-5" {
		t.Errorf("expected 'gpt-5', got '%s'", updated.DefaultModel)
	}

	// Resolve model
	if rm := ResolveOpenCodeModel("opencode/deepseek-v4-flash", updated); rm != "deepseek-v4-flash" {
		t.Errorf("expected 'deepseek-v4-flash', got '%s'", rm)
	}
	if rm := ResolveOpenCodeModel("oc/glm-5", updated); rm != "glm-5" {
		t.Errorf("expected 'glm-5', got '%s'", rm)
	}
	if rm := ResolveOpenCodeModel("", updated); rm != "gpt-5" {
		t.Errorf("expected fallback to DefaultModel 'gpt-5', got '%s'", rm)
	}

	// Import from mock local auth file
	mockAuthPath := filepath.Join(tmpDir, "mock_auth.json")
	if err := os.WriteFile(mockAuthPath, []byte(`{"opencode": {"type": "api", "key": "sk-imported-key"}}`), 0600); err != nil {
		t.Fatalf("WriteFile mock_auth.json failed: %v", err)
	}

	imported, err := m.ImportOpenCodeLocalAccount(mockAuthPath)
	if err != nil {
		t.Fatalf("ImportOpenCodeLocalAccount failed: %v", err)
	}
	if imported.AccessToken != "sk-imported-key" {
		t.Errorf("expected 'sk-imported-key', got '%s'", imported.AccessToken)
	}

	// Re-importing same key should update, not duplicate
	countBefore := 0
	for _, a := range m.GetAccounts() {
		if a.Provider == "opencode" {
			countBefore++
		}
	}
	if countBefore != 2 {
		t.Errorf("expected 2 opencode accounts, got %d", countBefore)
	}

	imported2, err := m.ImportOpenCodeLocalAccount(mockAuthPath)
	if err != nil {
		t.Fatalf("Re-import failed: %v", err)
	}
	if imported2.ID != imported.ID {
		t.Errorf("expected same ID on re-import, got %s vs %s", imported2.ID, imported.ID)
	}

	// Delete
	m.RemoveAccount(id)
	if m.GetAccountByID(id) != nil {
		t.Error("expected account to be deleted")
	}
}

func TestOpenCodeLBAndConcurrency(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "opencode_lb_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(tmpDir)
	})

	m := NewManager()
	m.Init(tmpDir)

	if m.GetOpenCodeLBMode() != "round-robin" {
		t.Errorf("expected default round-robin, got %s", m.GetOpenCodeLBMode())
	}
	m.SetOpenCodeLBMode("sticky")
	if m.GetOpenCodeLBMode() != "sticky" {
		t.Errorf("expected sticky, got %s", m.GetOpenCodeLBMode())
	}
	m.SetOpenCodeLBMode("invalid")
	if m.GetOpenCodeLBMode() != "round-robin" {
		t.Errorf("expected reset to round-robin on invalid mode, got %s", m.GetOpenCodeLBMode())
	}

	if m.GetOpenCodeMaxConcurrency() != defaultMaxConcurrency {
		t.Errorf("expected default %d, got %d", defaultMaxConcurrency, m.GetOpenCodeMaxConcurrency())
	}
	m.SetOpenCodeMaxConcurrency(25)
	if m.GetOpenCodeMaxConcurrency() != 25 {
		t.Errorf("expected 25, got %d", m.GetOpenCodeMaxConcurrency())
	}
}

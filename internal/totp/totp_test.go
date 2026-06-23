package totp

import (
	"testing"
)

func TestGenerateTOTP(t *testing.T) {
	// Standard secret: JBSWY3DPEHPK3PXP (base32 representation of "Hello!\x00\x00")
	secret := "JBSWY3DPEHPK3PXP"

	code, remaining, err := GenerateTOTP(secret)
	if err != nil {
		t.Fatalf("Failed to generate TOTP: %v", err)
	}

	if len(code) != 6 {
		t.Errorf("Expected code length to be 6, got %d (code: %s)", len(code), code)
	}

	// Code should only contain digits
	for _, char := range code {
		if char < '0' || char > '9' {
			t.Errorf("Expected code to contain only digits, got: %s", code)
			break
		}
	}

	if remaining < 1 || remaining > 30 {
		t.Errorf("Expected remaining seconds to be in range [1, 30], got %d", remaining)
	}

	// Test lowercase and spaces
	code2, _, err2 := GenerateTOTP("jbsw y3dp ehpk 3pxp")
	if err2 != nil {
		t.Fatalf("Failed to generate TOTP with lowercase and spaces: %v", err2)
	}
	if code != code2 {
		t.Errorf("Expected codes to match for different formatting, got %s and %s", code, code2)
	}

	// Test invalid secret
	_, _, err = GenerateTOTP("invalid secret with non-base32 chars like @!")
	if err == nil {
		t.Error("Expected error for invalid base32 secret, got nil")
	}
}

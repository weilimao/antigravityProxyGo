package quota

import "testing"

func TestAntigravityQuotaUsesCapturedFormatAndOwnProject(t *testing.T) {
	q := NewQuotaService()
	q.SetRequestTemplate(func(pathNeedle string) (map[string]string, []byte, bool) {
		switch pathNeedle {
		case "retrieveUserQuotaSummary":
			return map[string]string{
				"Accept-Encoding": "gzip",
				"Authorization":   "[REDACTED]",
				"Content-Type":    "application/json",
				"User-Agent":      "antigravity/hub/2.29.0 (aidev_client; os_type=windows; arch=amd64; cl=1013060670)",
			}, []byte(`{"project":"favorable-synapse-ttvcb"}`), true
		case "loadCodeAssist":
			return map[string]string{
				"User-Agent":   "antigravity/hub/2.29.0 (aidev_client; os_type=windows; arch=amd64; cl=1013060670)",
				"Content-Type": "application/json",
			}, []byte(`{"metadata":{"ideType":"ANTIGRAVITY","ideVersion":"2.29.0","platform":"WINDOWS_AMD64","ideName":"antigravity"}}`), true
		default:
			return nil, nil, false
		}
	})

	headers := q.antigravityUpstreamHeaders("retrieveUserQuotaSummary", "ya29.this-account")
	if headers["Authorization"] != "Bearer ya29.this-account" {
		t.Fatalf("authorization = %q", headers["Authorization"])
	}
	if headers["User-Agent"] != "antigravity/hub/2.29.0 (aidev_client; os_type=windows; arch=amd64; cl=1013060670)" {
		t.Fatalf("user-agent = %q", headers["User-Agent"])
	}
	if _, ok := headers["Accept-Encoding"]; ok {
		t.Fatal("Accept-Encoding must stay unset so the HTTP client can decompress gzip")
	}

	body := q.retrieveQuotaBody("account-project-1")
	if body["project"] != "account-project-1" {
		t.Fatalf("project = %#v", body["project"])
	}

	loadHeaders := q.antigravityUpstreamHeaders("loadCodeAssist", "ya29.this-account")
	loadBody := q.loadCodeAssistBody(loadHeaders["User-Agent"])
	metadata, _ := loadBody["metadata"].(map[string]interface{})
	if metadata["ideVersion"] != "2.29.0" || metadata["platform"] != "WINDOWS_AMD64" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestAntigravityQuotaFallsBackToDesktopCaptureShape(t *testing.T) {
	q := NewQuotaService()
	headers := q.antigravityUpstreamHeaders("retrieveUserQuotaSummary", "ya29.fallback")
	if headers["User-Agent"] != desktopQuotaUserAgent {
		t.Fatalf("user-agent = %q", headers["User-Agent"])
	}
	if headers["Content-Type"] != "application/json" {
		t.Fatalf("content-type = %q", headers["Content-Type"])
	}
	body := q.retrieveQuotaBody("account-project-2")
	if len(body) != 1 || body["project"] != "account-project-2" {
		t.Fatalf("body = %#v", body)
	}

	loadBody := q.loadCodeAssistBody(headers["User-Agent"])
	metadata, _ := loadBody["metadata"].(map[string]interface{})
	if metadata["ideVersion"] != "2.29.1" || metadata["platform"] != "WINDOWS_AMD64" || metadata["ideName"] != "antigravity" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestAntigravityQuotaIgnoresNonDesktopCapture(t *testing.T) {
	q := NewQuotaService()
	q.SetRequestTemplate(func(pathNeedle string) (map[string]string, []byte, bool) {
		return map[string]string{
			"User-Agent": "antigravity/2.6.0 windows/amd64 google-api-nodejs-client/10.3.0",
		}, []byte(`{"project":"other"}`), true
	})
	headers := q.antigravityUpstreamHeaders("retrieveUserQuotaSummary", "ya29.fallback")
	if headers["User-Agent"] != desktopQuotaUserAgent {
		t.Fatalf("user-agent = %q", headers["User-Agent"])
	}
}

func TestLoadCodeAssistBorrowsQuotaSummaryUserAgent(t *testing.T) {
	q := NewQuotaService()
	q.SetRequestTemplate(func(pathNeedle string) (map[string]string, []byte, bool) {
		if pathNeedle != "retrieveUserQuotaSummary" {
			return nil, nil, false
		}
		return map[string]string{
			"User-Agent": "antigravity/hub/9.9.9 (aidev_client; os_type=windows; arch=amd64; cl=1)",
		}, []byte(`{"project":"captured"}`), true
	})
	headers := q.antigravityUpstreamHeaders("loadCodeAssist", "ya29.borrow")
	if headers["User-Agent"] != "antigravity/hub/9.9.9 (aidev_client; os_type=windows; arch=amd64; cl=1)" {
		t.Fatalf("user-agent = %q", headers["User-Agent"])
	}
	metadata, _ := q.loadCodeAssistBody(headers["User-Agent"])["metadata"].(map[string]interface{})
	if metadata["ideVersion"] != "9.9.9" {
		t.Fatalf("ideVersion = %#v", metadata["ideVersion"])
	}
}

func TestAntigravityQuota_DynamicUAProvider(t *testing.T) {
	q := NewQuotaService()
	customUA := "antigravity/hub/2.35.0 (aidev_client; os_type=windows; arch=amd64; cl=9999999999)"
	q.SetAntigravityVersionProviders(
		func() string { return customUA },
		func() string { return "2.35.0" },
	)

	headers := q.antigravityUpstreamHeaders("retrieveUserQuotaSummary", "ya29.test")
	if headers["User-Agent"] != customUA {
		t.Fatalf("expected dynamic UA %q, got %q", customUA, headers["User-Agent"])
	}

	loadBody := q.loadCodeAssistBody(headers["User-Agent"])
	metadata, _ := loadBody["metadata"].(map[string]interface{})
	if metadata["ideVersion"] != "2.35.0" {
		t.Fatalf("expected ideVersion 2.35.0, got %#v", metadata["ideVersion"])
	}
}

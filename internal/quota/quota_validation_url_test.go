package quota

import (
	"testing"
)

func TestExtractValidationURL_StandardGoogleErrorInfo(t *testing.T) {
	rawJSON := []byte(`{
  "error": {
    "code": 403,
    "message": "Verify your account to continue.",
    "errors": [
      {
        "message": "Verify your account to continue.",
        "domain": "global",
        "reason": "forbidden"
      }
    ],
    "status": "PERMISSION_DENIED",
    "details": [
      {
        "@type": "type.googleapis.com/google.rpc.ErrorInfo",
        "reason": "VALIDATION_REQUIRED",
        "domain": "cloudcode-pa.googleapis.com",
        "metadata": {
          "validation_error_message": "Verify your account to continue.",
          "validation_url_link_text": "Verify your account",
          "validation_url": "https://accounts.google.com/signin/continue?sarp=1&scc=1&continue=https://developers.google.com/gemini-code-assist/auth/auth_success_gemini&plt=AKgnsbsqFZV9QxEfLB3_xPIOpSm0cIH43&flowName=GlifWebSignIn&authuser",
          "validation_learn_more_link_text": "Learn more",
          "validation_learn_more_url": "https://support.google.com/accounts?p=al_alert"
        }
      }
    ]
  }
}`)

	url := ExtractValidationURL(rawJSON)
	expected := "https://accounts.google.com/signin/continue?sarp=1&scc=1&continue=https://developers.google.com/gemini-code-assist/auth/auth_success_gemini&plt=AKgnsbsqFZV9QxEfLB3_xPIOpSm0cIH43&flowName=GlifWebSignIn&authuser"
	if url != expected {
		t.Fatalf("ExtractValidationURL failed: expected %q, got %q", expected, url)
	}
}

func TestExtractValidationURL_GoogleHelpLinks(t *testing.T) {
	rawJSON := []byte(`{
  "error": {
    "code": 403,
    "message": "Verify your account to continue.",
    "status": "PERMISSION_DENIED",
    "details": [
      {
        "@type": "type.googleapis.com/google.rpc.Help",
        "links": [
          {
            "description": "Verify your account",
            "url": "https://accounts.google.com/signin/continue?flowName=GlifWebSignIn&sarp=1"
          }
        ]
      }
    ]
  }
}`)

	url := ExtractValidationURL(rawJSON)
	expected := "https://accounts.google.com/signin/continue?flowName=GlifWebSignIn&sarp=1"
	if url != expected {
		t.Fatalf("ExtractValidationURL with Help links failed: expected %q, got %q", expected, url)
	}
}

func TestExtractValidationURL_EscapedJSON(t *testing.T) {
	rawJSON := []byte(`{"error":{"code":403,"details":[{"metadata":{"validation_url":"https://accounts.google.com/signin/continue?sarp=1\u0026scc=1"}}]}}`)

	url := ExtractValidationURL(rawJSON)
	expected := "https://accounts.google.com/signin/continue?sarp=1&scc=1"
	if url != expected {
		t.Fatalf("ExtractValidationURL escaped test failed: expected %q, got %q", expected, url)
	}
}

func TestExtractValidationURL_NonValidationError(t *testing.T) {
	normal429 := []byte(`{
  "error": {
    "code": 429,
    "message": "Resource has been exhausted (e.g. check quota).",
    "status": "RESOURCE_EXHAUSTED"
  }
}`)

	url := ExtractValidationURL(normal429)
	if url != "" {
		t.Fatalf("ExtractValidationURL should return empty for 429, got %q", url)
	}

	emptyJSON := []byte(``)
	if ExtractValidationURL(emptyJSON) != "" {
		t.Fatalf("ExtractValidationURL should return empty for empty input")
	}

	corruptJSON := []byte(`not-a-json`)
	if ExtractValidationURL(corruptJSON) != "" {
		t.Fatalf("ExtractValidationURL should return empty for corrupt input")
	}
}

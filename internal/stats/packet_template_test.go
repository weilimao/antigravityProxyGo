package stats

import (
	"encoding/json"
	"testing"
)

func TestLatestRequestTemplateReturnsNewestMatchingPacket(t *testing.T) {
	pc := NewPacketCapturer(nil, nil, func() bool { return true })
	pc.packets = []*CapturedPacket{
		{
			Path: "/v1internal:retrieveUserQuotaSummary",
			ReqHeaders: map[string]interface{}{
				"User-Agent":    "antigravity/hub/2.29.0 (aidev_client; os_type=windows; arch=amd64; cl=1013060670)",
				"Authorization": "[REDACTED]",
			},
			ReqBody: map[string]interface{}{"project": "favorable-synapse-ttvcb"},
		},
		{
			Path:       "/v1internal:retrieveUserQuotaSummary",
			ReqHeaders: map[string]string{"User-Agent": "antigravity/hub/1.0.0"},
			ReqBody:    json.RawMessage(`{"project":"old"}`),
		},
	}

	headers, body, ok := pc.LatestRequestTemplate("retrieveUserQuotaSummary")
	if !ok {
		t.Fatal("expected a template")
	}
	if headers["User-Agent"] != "antigravity/hub/2.29.0 (aidev_client; os_type=windows; arch=amd64; cl=1013060670)" {
		t.Fatalf("user-agent = %q", headers["User-Agent"])
	}
	if headers["Authorization"] != "[REDACTED]" {
		t.Fatalf("authorization = %q", headers["Authorization"])
	}
	var decoded map[string]string
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["project"] != "favorable-synapse-ttvcb" {
		t.Fatalf("project = %q", decoded["project"])
	}
}

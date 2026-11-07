package proxy

import "testing"

func TestReplaceTopLevelJSONStringKeepsSurroundingBytes(t *testing.T) {
	raw := []byte(`{"enabledCreditTypes":["GOOGLE_ONE_AI"],"model":"gemini-3.5-flash","project":"client-project","userAgent":"antigravity","request":{"contents":[{"parts":[{"text":"project client-project"}]}]}}`)
	got, ok := replaceTopLevelJSONString(raw, "project", "real-gcp-project")
	if !ok {
		t.Fatal("expected replace to succeed")
	}
	want := []byte(`{"enabledCreditTypes":["GOOGLE_ONE_AI"],"model":"gemini-3.5-flash","project":"real-gcp-project","userAgent":"antigravity","request":{"contents":[{"parts":[{"text":"project client-project"}]}]}}`)
	if string(got) != string(want) {
		t.Fatalf("bytes diverged\nwant: %s\ngot:  %s", want, got)
	}
}

func TestReplaceTopLevelJSONStringInsertsWithoutReordering(t *testing.T) {
	raw := []byte(`{"model":"gemini-3.5-flash","userAgent":"antigravity","request":{"project":"inner"}}`)
	got, ok := replaceTopLevelJSONString(raw, "project", "real-gcp-project")
	if !ok {
		t.Fatal("expected insert to succeed")
	}
	want := []byte(`{"project":"real-gcp-project","model":"gemini-3.5-flash","userAgent":"antigravity","request":{"project":"inner"}}`)
	if string(got) != string(want) {
		t.Fatalf("bytes diverged\nwant: %s\ngot:  %s", want, got)
	}
}

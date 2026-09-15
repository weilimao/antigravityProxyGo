package crypto

import (
	"testing"
)

func TestRelayHMACSign(t *testing.T) {
	secret := "geektools_relay_secret_key_888"
	params := map[string]interface{}{
		"relay_order_no": "ORD_TEST_12345",
		"amount_cents":   3900,
		"subject":        "Antigravity 会员订阅",
		"notify_url":     "http://example.com/notify",
		"return_url":     "http://example.com/return",
	}

	sign := GenerateRelaySign(params, secret)
	if sign == "" {
		t.Fatalf("expected non-empty signature, got empty")
	}

	// 验证自洽
	paramsWithSign := make(map[string]interface{})
	for k, v := range params {
		paramsWithSign[k] = v
	}
	paramsWithSign["sign"] = sign

	if !VerifyRelaySign(paramsWithSign, secret) {
		t.Fatalf("expected signature verification to pass, but failed")
	}

	// 篡改金额后应验签失败
	tampered := make(map[string]interface{})
	for k, v := range paramsWithSign {
		tampered[k] = v
	}
	tampered["amount_cents"] = 100 // 改小金额
	if VerifyRelaySign(tampered, secret) {
		t.Fatalf("expected signature verification to fail on tampered amount, but passed")
	}

	// 错误秘钥应验签失败
	if VerifyRelaySign(paramsWithSign, "wrong_secret") {
		t.Fatalf("expected signature verification to fail on wrong secret, but passed")
	}
}

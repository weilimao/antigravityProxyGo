package crypto

import (
	"testing"
)

func TestEpayMD5Sign(t *testing.T) {
	key := "my_epay_secret_key_123456"
	params := map[string]interface{}{
		"pid":          "1001",
		"type":         "alipay",
		"out_trade_no": "ORD_20260916_9999",
		"notify_url":   "http://example.com/notify",
		"return_url":   "http://example.com/return",
		"name":         "测试套餐购买",
		"money":        "39.00",
		"sign_type":    "MD5", // 应该被忽略
	}

	sign := GenerateEpaySign(params, key)
	if sign == "" {
		t.Fatalf("expected non-empty md5 sign, got empty")
	}

	// 验签通过测试
	paramsWithSign := make(map[string]interface{})
	for k, v := range params {
		paramsWithSign[k] = v
	}
	paramsWithSign["sign"] = sign

	if !VerifyEpaySign(paramsWithSign, key) {
		t.Fatalf("expected epay md5 sign verification to pass, but failed")
	}

	// 篡改金额断言失败
	tampered := make(map[string]interface{})
	for k, v := range paramsWithSign {
		tampered[k] = v
	}
	tampered["money"] = "0.01"
	if VerifyEpaySign(tampered, key) {
		t.Fatalf("expected verification to fail after tampering money, but passed")
	}

	// 密钥错误断言失败
	if VerifyEpaySign(paramsWithSign, "wrong_key_654321") {
		t.Fatalf("expected verification to fail with wrong key, but passed")
	}

	// 字符串重载测试
	stringMap := map[string]string{
		"pid":   "1001",
		"money": "39.00",
		"name":  "测试",
		"sign":  sign,
	}
	strSign := GenerateEpaySignString(stringMap, key)
	if strSign == "" {
		t.Fatalf("expected non-empty sign from string overload")
	}
}

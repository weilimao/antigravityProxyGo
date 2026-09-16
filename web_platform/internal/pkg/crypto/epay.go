package crypto

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// GenerateEpaySign 与 ProxySubForClash/payments.py 的 epay_sign 逻辑 100% 对齐:
// 参数按 key ASCII 升序 (排除 sign/sign_type/空值)，以 key=val& 拼接后末尾拼商户密钥，取 MD5 十六进制小写
func GenerateEpaySign(params map[string]interface{}, epayKey string) string {
	keys := make([]string, 0, len(params))
	filtered := make(map[string]string)

	for k, v := range params {
		if k == "sign" || k == "sign_type" || v == nil {
			continue
		}
		valStr := strings.TrimSpace(fmt.Sprintf("%v", v))
		if valStr == "" {
			continue
		}
		keys = append(keys, k)
		filtered[k] = valStr
	}

	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, filtered[k]))
	}

	raw := strings.Join(pairs, "&") + epayKey
	hash := md5.Sum([]byte(raw))
	return hex.EncodeToString(hash[:])
}

// GenerateEpaySignString 接受 map[string]string 参数的便捷重载
func GenerateEpaySignString(params map[string]string, epayKey string) string {
	m := make(map[string]interface{}, len(params))
	for k, v := range params {
		m[k] = v
	}
	return GenerateEpaySign(m, epayKey)
}

// VerifyEpaySign 校验易支付回调或请求签名 (彩虹易支付 MD5 协议)
func VerifyEpaySign(params map[string]interface{}, epayKey string) bool {
	signVal, ok := params["sign"]
	if !ok || signVal == nil || epayKey == "" {
		return false
	}
	signStr := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", signVal)))
	expected := strings.ToLower(GenerateEpaySign(params, epayKey))
	return signStr != "" && signStr == expected
}

// VerifyEpaySignString 校验 map[string]string 易支付签名
func VerifyEpaySignString(params map[string]string, epayKey string) bool {
	m := make(map[string]interface{}, len(params))
	for k, v := range params {
		m[k] = v
	}
	return VerifyEpaySign(m, epayKey)
}

package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// GenerateRelaySign 与 ProxySubForClash/resource_hub 的 generate_relay_sign 逻辑 100% 对齐:
// 过滤空值与 sign 字段，按 Key ASCII 升序排序，以 key=val& 拼接，计算 HMAC-SHA256 十六进制小写
func GenerateRelaySign(params map[string]interface{}, secret string) string {
	keys := make([]string, 0, len(params))
	filtered := make(map[string]string)

	for k, v := range params {
		if k == "sign" || v == nil {
			continue
		}
		valStr := fmt.Sprintf("%v", v)
		if strings.TrimSpace(valStr) == "" {
			continue
		}
		keys = append(keys, k)
		filtered[k] = valStr
	}

	sort.Strings(keys)

	var pairs []string
	for _, k := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, filtered[k]))
	}
	rawStr := strings.Join(pairs, "&")

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(rawStr))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyRelaySign 验证极客工坊签名
func VerifyRelaySign(params map[string]interface{}, secret string) bool {
	signVal, ok := params["sign"]
	if !ok || signVal == nil || secret == "" {
		return false
	}
	signStr := strings.ToLower(fmt.Sprintf("%v", signVal))
	expected := strings.ToLower(GenerateRelaySign(params, secret))
	return hmac.Equal([]byte(signStr), []byte(expected))
}

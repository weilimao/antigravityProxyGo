package relay

import (
	gojson "github.com/goccy/go-json"
)

// fast_json.go: 提供基于 goccy/go-json 的高性能零/低反射 JSON 序列化与反序列化封装。
// 对齐标准库 encoding/json 的语义，但在频繁的 SSE 流式 Chunk 编解码中大幅降低反射与 CPU 占用。

// fastMarshal 使用 goccy/go-json 快速序列化
func fastMarshal(v interface{}) ([]byte, error) {
	return gojson.Marshal(v)
}

// fastMarshalString 将任意结构快速序列化为紧凑 JSON 字符串。
// 失败时返回空字符串，与原 jsonString 行为一致。
func fastMarshalString(v interface{}) string {
	b, err := gojson.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// fastUnmarshal 使用 goccy/go-json 快速反序列化
func fastUnmarshal(data []byte, v interface{}) error {
	return gojson.Unmarshal(data, v)
}

package relay

import (
	"regexp"
	"strings"
)

var thoughtSigRegex = regexp.MustCompile(`(?s)\r?\n?<!--thought_signature:.*?-->`)

// SanitizeAllThoughtSignatures 清洗掉文本中残留的所有 thought_signature 注释标签
func SanitizeAllThoughtSignatures(text string) string {
	if !strings.Contains(text, "<!--thought_signature:") {
		return text
	}
	return thoughtSigRegex.ReplaceAllString(text, "")
}

func EncodeThoughtSignature(sig string) string {
	// 彻底禁用把签名暴露到纯文本中的行为
	return ""
}

func DecodeThoughtSignature(text string) (string, []string) {
	startStr := "<!--thought_signature:"
	endStr := "-->"

	var sigs []string
	cleanText := text

	for {
		startIdx := strings.Index(cleanText, startStr)
		if startIdx == -1 {
			break
		}

		endIdx := strings.Index(cleanText[startIdx:], endStr)
		if endIdx == -1 {
			break
		}
		endIdx += startIdx

		sig := cleanText[startIdx+len(startStr) : endIdx]
		sigs = append(sigs, sig)

		// Remove the signature from the text
		before := cleanText[:startIdx]
		after := cleanText[endIdx+len(endStr):]

		// If before ends with newline, and we just removed a line, clean it up optionally
		// But strictly just removing it is fine
		if len(before) > 0 && before[len(before)-1] == '\n' {
			before = before[:len(before)-1]
		}

		cleanText = before + after
	}

	return cleanText, sigs
}

// SanitizeInterruptedHistoryText 清洗客户端历史消息中残留的 "[Tool use interrupted]"、
// "[Tool interrupted]" 等中断标记文本，避免上游模型被历史中的中断标记带偏产生复读幻觉。
func SanitizeInterruptedHistoryText(text string) string {
	if text == "" {
		return ""
	}
	t := strings.TrimSpace(text)
	if t == "[Tool use interrupted]" || t == "● [Tool use interrupted]" || t == "[Tool interrupted]" || t == "● [Tool interrupted]" {
		return ""
	}
	cleaned := strings.ReplaceAll(text, "● [Tool use interrupted]", "")
	cleaned = strings.ReplaceAll(cleaned, "[Tool use interrupted]", "")
	cleaned = strings.ReplaceAll(cleaned, "● [Tool interrupted]", "")
	cleaned = strings.ReplaceAll(cleaned, "[Tool interrupted]", "")
	return strings.TrimSpace(cleaned)
}


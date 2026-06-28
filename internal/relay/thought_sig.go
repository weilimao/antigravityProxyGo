package relay

import (
	"strings"
)

func EncodeThoughtSignature(sig string) string {
	if sig == "" {
		return ""
	}
	return "\n<!--thought_signature:" + sig + "-->"
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

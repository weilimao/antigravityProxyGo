package relay

// compat_translate_types_gemini.go defines Gemini generateContent protocol types
// (Blob / Part / Content / Instruction / Candidate / UsageMetadata / Response),
// shared by Translate{OpenAI,Anthropic}ToGemini outputs, OCR and direct paths.
// Split from compat_translate.go, physical move only, line-for-line equivalent.

import (
	"encoding/json"
)

type GeminiBlob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type GeminiPart struct {
	Text             string                  `json:"text,omitempty"`
	InlineData       *GeminiBlob             `json:"inlineData,omitempty"`
	FunctionCall     *GeminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *GeminiFunctionResponse `json:"functionResponse,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
}

type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts,omitempty"`
}

type GeminiInstruction struct {
	Parts []GeminiPart `json:"parts"`
}

type GeminiCandidateContent struct {
	Parts []GeminiPart `json:"parts"`
	Role  string       `json:"role"`
}

type GeminiCandidate struct {
	Content      GeminiCandidateContent `json:"content"`
	FinishReason string                 `json:"finishReason"`
	Index        int                    `json:"index"`
}

type GeminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type GeminiResponse struct {
	Candidates    []GeminiCandidate   `json:"candidates"`
	UsageMetadata GeminiUsageMetadata `json:"usageMetadata"`
}

// UnmarshalJSON 实现了自适应套娃解包：兼容官方的扁平结构与云助手的 "response": {} 嵌套结构
func (g *GeminiResponse) UnmarshalJSON(data []byte) error {
	type Alias GeminiResponse
	var aux struct {
		*Alias
		Response *Alias `json:"response"`
	}
	aux.Alias = (*Alias)(g)
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Response != nil {
		if len(aux.Response.Candidates) > 0 {
			g.Candidates = aux.Response.Candidates
		}
		if aux.Response.UsageMetadata.PromptTokenCount > 0 || aux.Response.UsageMetadata.CandidatesTokenCount > 0 {
			g.UsageMetadata = aux.Response.UsageMetadata
		}
	}
	return nil
}

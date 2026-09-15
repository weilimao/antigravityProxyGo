package model

import (
	"time"
)

type Setting struct {
	Key         string    `gorm:"primaryKey;size:64" json:"key"`
	Value       string    `gorm:"type:text" json:"value"`
	Description string    `gorm:"size:255" json:"description"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// ModelMappingEntry 与桌面端 internal/settings/settings.go 100% 结构对齐
type ModelMappingEntry struct {
	ClientModel              string   `json:"clientModel"`
	TargetModel              string   `json:"targetModel"`
	Expose                   bool     `json:"expose"`
	TargetProvider           string   `json:"targetProvider,omitempty"`
	TargetGroupID            string   `json:"targetGroupId,omitempty"`
	TargetFormat             string   `json:"targetFormat,omitempty"`
	OwnedBy                  string   `json:"ownedBy,omitempty"`
	InjectChatTemplateKwargs *bool    `json:"injectChatTemplateKwargs,omitempty"`
	Multimodal               *bool    `json:"multimodal,omitempty"`
	MaxInputTokens           *int64   `json:"maxInputTokens,omitempty"`
	VariantEfforts           []string `json:"variantEfforts,omitempty"`
	CandidateModels          []string `json:"candidateModels,omitempty"`
	UseBenchmarkPool         *bool    `json:"useBenchmarkPool,omitempty"`
}

// AutoRacingConfig 与桌面端 UserAutoConfig 对齐
type AutoRacingConfig struct {
	Enabled          bool     `json:"enabled"`
	CandidateModels  []string `json:"candidateModels"`
	UseBenchmarkPool bool     `json:"useBenchmarkPool"`
}

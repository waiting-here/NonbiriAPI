// Package language selects platform-owned bilingual text.
package language

import "strings"

// NormalizeLanguage preserves the platform's language selection: zh and zh-*
// select Chinese; all other values select English. Preference lists and
// surrounding whitespace are intentionally not interpreted.
func NormalizeLanguage(value string) string {
	if strings.EqualFold(value, "zh") || strings.HasPrefix(strings.ToLower(value), "zh-") {
		return "zh"
	}
	return "en"
}

func Select(value, zh, en string) string {
	if NormalizeLanguage(value) == "zh" {
		return zh
	}
	return en
}

// Bilingual keeps the existing JSON projection for platform-owned text. It is
// separate from stored administrator content and historical restriction text.
type Bilingual struct {
	Zh string `json:"zh"`
	En string `json:"en"`
}

func (text Bilingual) Pick(value string) string {
	return Select(value, text.Zh, text.En)
}

package contract

// ConfigurationType groups historical names in the setup UI without rewriting
// their persisted identity or widening their omitted-field service selection.
func (t Type) ConfigurationType() Type {
	if t.OpenAIServiceFamily() {
		return TypeOpenAICompatible
	}
	return t
}

func SupportedRerankFormats(kind Type) ([]RerankFormat, error) {
	profile, err := serviceProfile(kind)
	if err != nil {
		return nil, err
	}
	if kind.OpenAIServiceFamily() {
		return []RerankFormat{RerankFormatGeneric, RerankFormatCohere, RerankFormatJina, RerankFormatVoyage, RerankFormatSiliconFlow}, nil
	}
	if profile.fixedRerank != "" {
		return []RerankFormat{profile.fixedRerank}, nil
	}
	return []RerankFormat{}, nil
}

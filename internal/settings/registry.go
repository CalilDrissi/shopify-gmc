package settings

const (
	KeyAIBaseURL          = "ai_base_url"
	KeyAIAPIKey           = "ai_api_key"
	KeyAIModel            = "ai_model"
	KeyAIModelSummary     = "ai_model_summary"
	KeyAIMaxCallsPerAudit = "ai_max_calls_per_audit"
)

func DefaultRegistry() []Definition {
	return []Definition{
		{Key: KeyAIBaseURL, EnvVar: "AI_BASE_URL",
			Description: "Base URL for the AI provider"},
		{Key: KeyAIAPIKey, EnvVar: "AI_API_KEY", Secret: true,
			Description: "API key for the AI provider"},
		{Key: KeyAIModel, EnvVar: "AI_MODEL",
			Description: "Default AI model identifier"},
		{Key: KeyAIModelSummary, EnvVar: "AI_MODEL_SUMMARY",
			Description: "Model used for short summaries"},
		{Key: KeyAIMaxCallsPerAudit, EnvVar: "AI_MAX_CALLS_PER_AUDIT",
			Description: "Maximum AI calls allowed per audit"},
	}
}

func defaultRegistryMap() map[string]Definition {
	defs := DefaultRegistry()
	m := make(map[string]Definition, len(defs))
	for _, d := range defs {
		m[d.Key] = d
	}
	return m
}

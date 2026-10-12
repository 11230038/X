package config

import (
	"strings"
	"testing"
	"time"
)

func validLLMConfig() LLMConfig {
	return LLMConfig{
		Provider:      "deepseek",
		BaseURL:       "https://api.deepseek.com",
		Model:         "deepseek-chat",
		APIKey:        testLLMAPIKey,
		TaskModel:     "deepseek-chat",
		TaskMaxTokens: 512,
		Timeout:       2 * time.Minute,
		Capabilities: map[string]CapabilityParams{
			"chat": {
				Temperature:         0.7,
				ContextBudgetTokens: 32000,
				Stages: map[string]StageParams{
					"exploring":  {MaxTokens: 1024},
					"responding": {MaxTokens: 4096},
				},
			},
		},
	}
}

func TestLLMConfigValidateAcceptsTheShippedShape(t *testing.T) {
	if err := validLLMConfig().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestLLMConfigValidateRejectsInvalidSettings(t *testing.T) {
	withChat := func(mutate func(*CapabilityParams)) func(*LLMConfig) {
		return func(c *LLMConfig) {
			chat := c.Capabilities["chat"]
			mutate(&chat)
			c.Capabilities["chat"] = chat
		}
	}

	cases := []struct {
		name   string
		mutate func(*LLMConfig)
	}{
		{name: "unknown provider", mutate: func(c *LLMConfig) { c.Provider = "openai" }},
		{name: "relative base URL", mutate: func(c *LLMConfig) { c.BaseURL = "api.deepseek.com" }},
		{name: "base URL without a host", mutate: func(c *LLMConfig) { c.BaseURL = "https://" }},
		{name: "non-HTTP scheme", mutate: func(c *LLMConfig) { c.BaseURL = "ftp://api.deepseek.com" }},
		{name: "blank model", mutate: func(c *LLMConfig) { c.Model = "  " }},
		{name: "blank API key", mutate: func(c *LLMConfig) { c.APIKey = "" }},
		{name: "blank task model", mutate: func(c *LLMConfig) { c.TaskModel = "" }},
		{name: "zero timeout", mutate: func(c *LLMConfig) { c.Timeout = 0 }},
		{name: "zero task max tokens", mutate: func(c *LLMConfig) { c.TaskMaxTokens = 0 }},
		{name: "no capabilities", mutate: func(c *LLMConfig) { c.Capabilities = nil }},
		{name: "capability without stages", mutate: func(c *LLMConfig) {
			c.Capabilities["chat"] = CapabilityParams{Temperature: 0.5, ContextBudgetTokens: 32000}
		}},
		{name: "blank capability name", mutate: func(c *LLMConfig) {
			c.Capabilities[" "] = CapabilityParams{
				Temperature:         0.5,
				ContextBudgetTokens: 32000,
				Stages:              map[string]StageParams{"responding": {MaxTokens: 1}},
			}
		}},
		{name: "temperature above the range", mutate: withChat(func(p *CapabilityParams) { p.Temperature = 2.5 })},
		{name: "temperature below the range", mutate: withChat(func(p *CapabilityParams) { p.Temperature = -0.1 })},
		{name: "zero context budget", mutate: withChat(func(p *CapabilityParams) { p.ContextBudgetTokens = 0 })},
		{name: "negative context budget", mutate: withChat(func(p *CapabilityParams) { p.ContextBudgetTokens = -1 })},
		{name: "blank stage name", mutate: withChat(func(p *CapabilityParams) {
			p.Stages[" "] = StageParams{MaxTokens: 1}
		})},
		{name: "stage without max tokens", mutate: withChat(func(p *CapabilityParams) {
			p.Stages["responding"] = StageParams{}
		})},
		{name: "negative max tokens", mutate: withChat(func(p *CapabilityParams) {
			p.Stages["responding"] = StageParams{MaxTokens: -1}
		})},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validLLMConfig()
			tt.mutate(&cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil, want an error")
			}
			if strings.Contains(err.Error(), testLLMAPIKey) {
				t.Fatal("validation error exposed the LLM API key")
			}
		})
	}
}

func TestLoadParsesCapabilityParameters(t *testing.T) {
	values := validValues()
	values["LLM_CAPABILITY_CONFIG_JSON"] = `'{"chat":{"temperature":0.2,"context_budget_tokens":32000,"stages":{"exploring":{"max_tokens":256}}}}'`

	cfg, err := Load(writeDotenv(t, values))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	chat := cfg.LLM.Capabilities["chat"]
	if chat.Temperature != 0.2 {
		t.Errorf("temperature = %v, want 0.2", chat.Temperature)
	}
	if chat.ContextBudgetTokens != 32000 {
		t.Errorf("context_budget_tokens = %d, want 32000", chat.ContextBudgetTokens)
	}
	if chat.Stages["exploring"].MaxTokens != 256 {
		t.Errorf("exploring max_tokens = %d, want 256", chat.Stages["exploring"].MaxTokens)
	}
	if len(cfg.LLM.Capabilities) != 1 {
		t.Errorf("capabilities = %v, want only chat", cfg.LLM.Capabilities)
	}
}

func TestLoadRejectsMalformedCapabilityParameters(t *testing.T) {
	cases := []struct{ name, value string }{
		{name: "not JSON", value: "not json"},
		{name: "array instead of object", value: "[]"},
		{name: "empty object", value: "{}"},
		{name: "trailing content", value: `{"chat":{"temperature":0.5,"context_budget_tokens":32000,"stages":{"responding":{"max_tokens":1}}}} extra`},
		{name: "missing temperature", value: `{"chat":{"stages":{"responding":{"max_tokens":1}}}}`},
		{name: "missing max tokens", value: `{"chat":{"temperature":0.5,"context_budget_tokens":32000,"stages":{"responding":{}}}}`},
		{name: "missing stages", value: `{"chat":{"temperature":0.5}}`},
		{name: "unknown nested field", value: `{"chat":{"temperature":0.5,"context_budget_tokens":32000,"stages":{"responding":{"max_tokens":1}},"top_p":0.9}}`},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			values := validValues()
			values["LLM_CAPABILITY_CONFIG_JSON"] = "'" + tt.value + "'"

			if _, err := Load(writeDotenv(t, values)); err == nil {
				t.Fatalf("Load() error = nil, want an error for %s", tt.name)
			}
		})
	}
}

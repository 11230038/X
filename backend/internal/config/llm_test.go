package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func validLLMConfig() LLMConfig {
	return LLMConfig{
		Provider:            "deepseek",
		BaseURL:             "https://api.deepseek.com",
		Model:               "deepseek-chat",
		APIKeys:             []string{testLLMAPIKey},
		KeyFailureThreshold: 3,
		KeyCooldown:         time.Minute,
		TaskModel:           "deepseek-chat",
		TaskMaxTokens:       512,
		Timeout:             2 * time.Minute,
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
		{name: "no keys at all", mutate: func(c *LLMConfig) { c.APIKeys = nil }},
		{name: "an empty entry in the list", mutate: func(c *LLMConfig) { c.APIKeys = []string{testLLMAPIKey, ""} }},
		{name: "a key that is too short", mutate: func(c *LLMConfig) { c.APIKeys = []string{"too-short"} }},
		{name: "zero failure threshold", mutate: func(c *LLMConfig) { c.KeyFailureThreshold = 0 }},
		{name: "zero cooldown", mutate: func(c *LLMConfig) { c.KeyCooldown = 0 }},
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

// The keys are a list, and the list is what a deployment edits when it adds a
// second credential: one key behaves exactly as it did before, and two are read
// in the order they were written.
func TestLoadParsesTheKeyList(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value string
		want  []string
	}{
		{name: "one key", value: testLLMAPIKey, want: []string{testLLMAPIKey}},
		{name: "two keys", value: "first-unit-test-key,second-unit-test-key",
			want: []string{"first-unit-test-key", "second-unit-test-key"}},
		// The whitespace around a separator is the separator's, not the secret's:
		// `a, b` is how a person writes a list, and refusing it would be refusing
		// the ordinary spelling.
		{name: "two keys with a space after the comma", value: "first-unit-test-key, second-unit-test-key",
			want: []string{"first-unit-test-key", "second-unit-test-key"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			values := validValues()
			values["LLM_API_KEYS"] = tt.value

			cfg, err := Load(writeDotenv(t, values))
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if !slices.Equal(cfg.LLM.APIKeys, tt.want) {
				t.Errorf("APIKeys = %v, want %v", cfg.LLM.APIKeys, tt.want)
			}
			if cfg.LLM.KeyFailureThreshold != 3 {
				t.Errorf("KeyFailureThreshold = %d, want 3", cfg.LLM.KeyFailureThreshold)
			}
			if cfg.LLM.KeyCooldown != time.Minute {
				t.Errorf("KeyCooldown = %v, want 1m", cfg.LLM.KeyCooldown)
			}
		})
	}
}

// A list this service cannot use stops startup, and the failure says which
// position is wrong and never what is at it.
func TestLoadRefusesAKeyListItCannotUse(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "nothing at all", value: " ", want: "at least one key"},
		// A trailing comma is a list somebody was still editing. Reading it as one
		// key would be answering a question the file did not ask.
		{name: "a trailing comma", value: testLLMAPIKey + ",", want: "position 2"},
		{name: "a stub key", value: testLLMAPIKey + ",sk-live-stub", want: "position 2"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			values := validValues()
			values["LLM_API_KEYS"] = tt.value

			_, err := Load(writeDotenv(t, values))
			if err == nil {
				t.Fatalf("Load() error = nil, want an error for %s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to carry %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "sk-live") {
				t.Errorf("the error carries a key: %s", err)
			}
		})
	}
}

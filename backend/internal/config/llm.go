package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

// providerDeepSeek is the only model provider this version talks to. The wire
// protocol is OpenAI-compatible, but the name is still checked so that a typo
// stops startup instead of reaching the client as an unknown provider.
const providerDeepSeek = "deepseek"

// LLMConfig holds the model provider settings plus the per-capability tuning
// that CFG-01 asks for. Tuning lives here rather than in the capability
// manifests because configuration for this service is dotenv-only.
type LLMConfig struct {
	Provider string
	BaseURL  string
	Model    string
	APIKey   string
	// TaskModel serves the short internal calls that the user never explicitly
	// asked for, such as naming a session (CFG-02).
	TaskModel     string
	TaskMaxTokens int
	Timeout       time.Duration
	Capabilities  map[string]CapabilityParams
}

// CapabilityParams is the CFG-01 tuning for a single capability.
type CapabilityParams struct {
	Temperature float64
	Stages      map[string]StageParams
}

// StageParams is the CFG-01 tuning for a single stage of a capability.
type StageParams struct {
	MaxTokens int
}

// Validate checks the model provider settings. The API key is only checked for
// presence here and is never echoed back in a failure message.
func (c LLMConfig) Validate() error {
	if c.Provider != providerDeepSeek {
		return fmt.Errorf("LLM_PROVIDER must be %s", providerDeepSeek)
	}
	if err := validateBaseURL(c.BaseURL); err != nil {
		return fmt.Errorf("invalid LLM_BASE_URL: %w", err)
	}
	for key, value := range map[string]string{
		"LLM_MODEL":      c.Model,
		"LLM_API_KEY":    c.APIKey,
		"LLM_TASK_MODEL": c.TaskModel,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s must not be empty", key)
		}
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("LLM_TIMEOUT must be a positive duration")
	}
	if c.TaskMaxTokens <= 0 {
		return fmt.Errorf("LLM_TASK_MAX_TOKENS must be a positive integer")
	}
	return validateCapabilityParams(c.Capabilities)
}

func validateCapabilityParams(capabilities map[string]CapabilityParams) error {
	if len(capabilities) == 0 {
		return fmt.Errorf("LLM_CAPABILITY_CONFIG_JSON must configure at least one capability")
	}
	for name, params := range capabilities {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("LLM_CAPABILITY_CONFIG_JSON: capability name must not be blank")
		}
		if params.Temperature < 0 || params.Temperature > 2 {
			return fmt.Errorf("LLM_CAPABILITY_CONFIG_JSON: %s temperature must be between 0 and 2", name)
		}
		if len(params.Stages) == 0 {
			return fmt.Errorf("LLM_CAPABILITY_CONFIG_JSON: %s must configure at least one stage", name)
		}
		for stage, stageParams := range params.Stages {
			if strings.TrimSpace(stage) == "" {
				return fmt.Errorf("LLM_CAPABILITY_CONFIG_JSON: %s stage name must not be blank", name)
			}
			if stageParams.MaxTokens <= 0 {
				return fmt.Errorf("LLM_CAPABILITY_CONFIG_JSON: %s.%s max_tokens must be a positive integer", name, stage)
			}
		}
	}
	return nil
}

func loadLLMConfig(values map[string]string) (LLMConfig, error) {
	timeout, err := parsePositiveDuration(values, "LLM_TIMEOUT")
	if err != nil {
		return LLMConfig{}, err
	}
	taskMaxTokens, err := parsePositiveInt(values, "LLM_TASK_MAX_TOKENS")
	if err != nil {
		return LLMConfig{}, err
	}
	capabilities, err := parseCapabilityParams(values["LLM_CAPABILITY_CONFIG_JSON"])
	if err != nil {
		return LLMConfig{}, err
	}
	return LLMConfig{
		Provider: strings.TrimSpace(values["LLM_PROVIDER"]),
		// A trailing slash would double up when the client appends its path.
		BaseURL: strings.TrimSuffix(strings.TrimSpace(values["LLM_BASE_URL"]), "/"),
		Model:   strings.TrimSpace(values["LLM_MODEL"]),
		// The key is used verbatim: trimming it would silently change the secret.
		APIKey:        values["LLM_API_KEY"],
		TaskModel:     strings.TrimSpace(values["LLM_TASK_MODEL"]),
		TaskMaxTokens: taskMaxTokens,
		Timeout:       timeout,
		Capabilities:  capabilities,
	}, nil
}

// capabilityParamsJSON mirrors the documented shape of
// LLM_CAPABILITY_CONFIG_JSON. Pointers separate "absent" from a legitimate zero
// value, so a missing temperature cannot pass as 0.
type capabilityParamsJSON struct {
	Temperature *float64                   `json:"temperature"`
	Stages      map[string]stageParamsJSON `json:"stages"`
}

type stageParamsJSON struct {
	MaxTokens *int `json:"max_tokens"`
}

func parseCapabilityParams(raw string) (map[string]CapabilityParams, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	// Unknown fields are rejected so a misspelt key stops startup rather than
	// quietly falling back to a zero value.
	decoder.DisallowUnknownFields()
	var decoded map[string]capabilityParamsJSON
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("LLM_CAPABILITY_CONFIG_JSON must be a JSON object keyed by capability name")
	}
	// Anything after the object means the value was not a single object.
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("LLM_CAPABILITY_CONFIG_JSON must contain a single JSON object")
	}
	if err := validateCapabilityShape(decoded); err != nil {
		return nil, err
	}
	capabilities := make(map[string]CapabilityParams, len(decoded))
	for name, entry := range decoded {
		stages := make(map[string]StageParams, len(entry.Stages))
		for stage, stageEntry := range entry.Stages {
			stages[stage] = StageParams{MaxTokens: *stageEntry.MaxTokens}
		}
		capabilities[name] = CapabilityParams{Temperature: *entry.Temperature, Stages: stages}
	}
	return capabilities, nil
}

func validateCapabilityShape(decoded map[string]capabilityParamsJSON) error {
	if len(decoded) == 0 {
		return fmt.Errorf("LLM_CAPABILITY_CONFIG_JSON must configure at least one capability")
	}
	for name, entry := range decoded {
		if entry.Temperature == nil {
			return fmt.Errorf("LLM_CAPABILITY_CONFIG_JSON: %s must set temperature", name)
		}
		if len(entry.Stages) == 0 {
			return fmt.Errorf("LLM_CAPABILITY_CONFIG_JSON: %s must set at least one stage", name)
		}
		for stage, stageEntry := range entry.Stages {
			if stageEntry.MaxTokens == nil {
				return fmt.Errorf("LLM_CAPABILITY_CONFIG_JSON: %s.%s must set max_tokens", name, stage)
			}
		}
	}
	return nil
}

func validateBaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("must be an absolute URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https")
	}
	if parsed.Host == "" {
		return fmt.Errorf("must include a host")
	}
	return nil
}

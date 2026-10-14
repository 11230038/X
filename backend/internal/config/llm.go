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
	// APIKeys is the pool: one or more credentials for the same provider, used in
	// turn (§17.3). The key is a list rather than a single secret because a
	// provider's rate limit is per credential, so the second key is what keeps a
	// busy deployment answering.
	APIKeys []string
	// KeyFailureThreshold is how many consecutive rate limits rest one key, and
	// KeyCooldown is how long it rests.
	KeyFailureThreshold int
	KeyCooldown         time.Duration
	// TaskModel serves the short internal calls that the user never explicitly
	// asked for, such as naming a session (CFG-02).
	TaskModel     string
	TaskMaxTokens int
	Timeout       time.Duration
	Capabilities  map[string]CapabilityParams
}

// minAPIKeyLength is the shortest thing this service will accept as a provider
// key. It is a length rather than a pattern: providers differ in what their keys
// look like, and a deployment that pasted a placeholder — or a key with the
// middle missing — is caught by being obviously too short rather than by this
// service inventing a shape to match.
const minAPIKeyLength = 16

// CapabilityParams is the CFG-01 tuning for a single capability.
type CapabilityParams struct {
	Temperature float64
	// ContextBudgetTokens bounds the conversation the capability may send in one
	// model call. It sits with the rest of CFG-01's per-capability runtime
	// limits rather than being derived from a model name, which would put a
	// window size in code that no deployment could adjust.
	ContextBudgetTokens int
	Stages              map[string]StageParams
}

// StageParams is the CFG-01 tuning for a single stage of a capability.
type StageParams struct {
	MaxTokens int
}

// Validate checks the model provider settings. The keys are checked for shape
// and never echoed back: a failure message is read in places a secret should not
// reach, so it names the position of the key that is wrong and not the key.
func (c LLMConfig) Validate() error {
	if c.Provider != providerDeepSeek {
		return fmt.Errorf("LLM_PROVIDER must be %s", providerDeepSeek)
	}
	if err := validateBaseURL(c.BaseURL); err != nil {
		return fmt.Errorf("invalid LLM_BASE_URL: %w", err)
	}
	// An ordered list rather than a map, so that a file with two things wrong
	// reports the same one every time: which key is named is a fact about the
	// file, and a map would make it a fact about the run.
	for _, entry := range []struct{ key, value string }{
		{key: "LLM_MODEL", value: c.Model},
		{key: "LLM_TASK_MODEL", value: c.TaskModel},
	} {
		if strings.TrimSpace(entry.value) == "" {
			return fmt.Errorf("%s must not be empty", entry.key)
		}
	}
	if err := validateAPIKeys(c.APIKeys); err != nil {
		return err
	}
	if c.KeyFailureThreshold < 1 {
		return fmt.Errorf("LLM_KEY_FAILURE_THRESHOLD must be at least 1")
	}
	if c.KeyCooldown <= 0 {
		return fmt.Errorf("LLM_KEY_COOLDOWN must be a positive duration")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("LLM_TIMEOUT must be a positive duration")
	}
	if c.TaskMaxTokens <= 0 {
		return fmt.Errorf("LLM_TASK_MAX_TOKENS must be a positive integer")
	}
	return validateCapabilityParams(c.Capabilities)
}

// validateAPIKeys checks the shape of the pool.
//
// An entry that is empty is named as empty rather than as too short: a trailing
// comma or two commas in a row is a list somebody was still editing, and saying
// so is more use than "shorter than 16 characters", which is also true.
func validateAPIKeys(keys []string) error {
	if len(keys) == 0 {
		return fmt.Errorf("LLM_API_KEYS must list at least one key")
	}
	for i, key := range keys {
		switch {
		case key == "":
			return fmt.Errorf("LLM_API_KEYS: the key at position %d is empty", i+1)
		case len(key) < minAPIKeyLength:
			return fmt.Errorf("LLM_API_KEYS: the key at position %d is shorter than %d characters", i+1, minAPIKeyLength)
		}
	}
	return nil
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
		if params.ContextBudgetTokens <= 0 {
			return fmt.Errorf("LLM_CAPABILITY_CONFIG_JSON: %s context_budget_tokens must be a positive integer", name)
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
	keyThreshold, err := parsePositiveInt(values, "LLM_KEY_FAILURE_THRESHOLD")
	if err != nil {
		return LLMConfig{}, err
	}
	keyCooldown, err := parsePositiveDuration(values, "LLM_KEY_COOLDOWN")
	if err != nil {
		return LLMConfig{}, err
	}
	return LLMConfig{
		Provider: strings.TrimSpace(values["LLM_PROVIDER"]),
		// A trailing slash would double up when the client appends its path.
		BaseURL: strings.TrimSuffix(strings.TrimSpace(values["LLM_BASE_URL"]), "/"),
		Model:   strings.TrimSpace(values["LLM_MODEL"]),
		// The keys keep their own characters — parseList trims the whitespace
		// around a separator rather than inside a secret, which is the only way a
		// comma-separated list can be written and read back.
		APIKeys:             parseList(values["LLM_API_KEYS"]),
		KeyFailureThreshold: keyThreshold,
		KeyCooldown:         keyCooldown,
		TaskModel:           strings.TrimSpace(values["LLM_TASK_MODEL"]),
		TaskMaxTokens:       taskMaxTokens,
		Timeout:             timeout,
		Capabilities:        capabilities,
	}, nil
}

// capabilityParamsJSON mirrors the documented shape of
// LLM_CAPABILITY_CONFIG_JSON. Pointers separate "absent" from a legitimate zero
// value, so a missing temperature cannot pass as 0.
type capabilityParamsJSON struct {
	Temperature *float64                   `json:"temperature"`
	Budget      *int                       `json:"context_budget_tokens"`
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
		capabilities[name] = CapabilityParams{
			Temperature:         *entry.Temperature,
			ContextBudgetTokens: *entry.Budget,
			Stages:              stages,
		}
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
		if entry.Budget == nil {
			return fmt.Errorf("LLM_CAPABILITY_CONFIG_JSON: %s must set context_budget_tokens", name)
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

// Package groq presets the OpenAI-compatible adapter for Groq's API:
// the base URL, a default model, and a required API key are the only
// differences from any OpenAI-compatible endpoint. Everything else —
// tools, structured output, streaming, reasoning extraction — comes
// from providers/openai unchanged, because Groq speaks the same wire
// protocol.
//
// The package is a preset, not a protocol: a second HTTP client would
// be a second place to fix wire bugs.
package groq

import (
	"errors"
	"strings"

	"github.com/abubakarsiddik31/golem/providers/openai"
)

// DefaultBaseURL is Groq's OpenAI-compatible endpoint prefix.
const DefaultBaseURL = "https://api.groq.com/openai/v1"

// Model names seen on Groq's catalog. Groq rotates models faster than
// a release cycle; treat these as conveniences, not a catalog, and set
// Config.Model to whatever the console currently lists.
const (
	ModelLlama3370B             = "llama-3.3-70b-versatile"
	ModelDeepSeekR1DistillLlama = "deepseek-r1-distill-llama-70b"
)

// Preset fills cfg's BaseURL when unset and verifies the API key, so
// callers can inspect exactly what New will build. New calls it for
// you.
func Preset(cfg openai.Config) (openai.Config, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return openai.Config{}, errors.New("groq: API key is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	return cfg, nil
}

// New returns an OpenAI-compatible client preset for Groq: the base
// URL defaults to DefaultBaseURL, and the API key is required. Model
// should name a model from Groq's catalog; see the package constants
// for conveniences.
func New(cfg openai.Config) (*openai.Client, error) {
	cfg, err := Preset(cfg)
	if err != nil {
		return nil, err
	}
	return openai.New(cfg)
}

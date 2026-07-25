package api

import (
	protocol "oops/internal/agent/ai"

	"github.com/cloudwego/eino/components/model"
)

const (
	ContextSafetyTokens = 4096
	minimumMaxTokens    = 1
)

const (
	ThinkingLevelMinimal = "minimal"
	ThinkingLevelLow     = "low"
	ThinkingLevelMedium  = "medium"
	ThinkingLevelHigh    = "high"
	ThinkingLevelXHigh   = "xhigh"
)

// Options configures the call-time parameters shared by API adapters.
type Options struct {
	ContextWindow int
	MaxTokens     int
	Temperature   *float32
	Headers       map[string]string
}

// ClampMaxTokensToContext reserves capacity for the current context and protocol overhead.
func ClampMaxTokensToContext(contextWindow int, ctx protocol.Context, maxTokens int) int {
	if maxTokens < minimumMaxTokens {
		maxTokens = minimumMaxTokens
	}
	if contextWindow <= 0 {
		return maxTokens
	}
	available := contextWindow - protocol.EstimateContextTokens(ctx) - ContextSafetyTokens
	return min(maxTokens, max(minimumMaxTokens, available))
}

// BuildBaseOptions translates shared request settings into Eino call options.
func BuildBaseOptions(options Options, ctx protocol.Context) []model.Option {
	var out []model.Option
	if options.Temperature != nil {
		out = append(out, model.WithTemperature(*options.Temperature))
	}
	if options.MaxTokens > 0 {
		out = append(out, model.WithMaxTokens(ClampMaxTokensToContext(options.ContextWindow, ctx, options.MaxTokens)))
	}
	return out
}

// MaxTokensForContext returns the configured output cap after reserving context capacity.
func MaxTokensForContext(options Options, ctx protocol.Context) (int, bool) {
	if options.MaxTokens <= 0 {
		return 0, false
	}
	return ClampMaxTokensToContext(options.ContextWindow, ctx, options.MaxTokens), true
}

// ClampReasoning maps the highest shared thinking level to the highest level supported by adapters here.
func ClampReasoning(level string) string {
	if level == ThinkingLevelXHigh {
		return ThinkingLevelHigh
	}
	return level
}

package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

type textTokenCounter interface {
	Count(text string) int64
}

type tokenEncoder interface {
	Encode(text string, allowedSpecial []string, disallowedSpecial []string) []int
}

type tokenEstimator struct {
	encoder tokenEncoder
}

type encoderCache struct {
	mu       sync.RWMutex
	encoders map[string]tokenEncoder
}

var sharedEncoderCache = &encoderCache{
	encoders: map[string]tokenEncoder{},
}

func EnrichTraceJSON(raw json.RawMessage) (json.RawMessage, error) {
	trace, err := parseStoredTrace(raw)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(trace)
	if err != nil {
		return nil, fmt.Errorf("marshal enriched trace: %w", err)
	}
	return payload, nil
}

func enrichStoredTrace(trace StoredTrace) StoredTrace {
	return estimateTraceTokens(trace, newTokenEstimator(trace.Model))
}

func estimateTraceTokens(trace StoredTrace, counter textTokenCounter) StoredTrace {
	trace.EstimatedUsage = StoredEstimatedUsage{}
	if counter == nil {
		return trace
	}

	trace.EstimatedUsage.SystemPrompt = counter.Count(trace.SystemPrompt)

	for index := range trace.Messages {
		message := &trace.Messages[index]
		message.EstimatedTokens = 0

		switch message.Role {
		case "user":
			message.EstimatedTokens = counter.Count(message.Content)
			trace.EstimatedUsage.User += message.EstimatedTokens
		case "assistant":
			contentTokens := counter.Count(message.Content)
			reasoningTokens := counter.Count(message.Reasoning)
			message.EstimatedTokens = contentTokens + reasoningTokens
			trace.EstimatedUsage.Assistant += contentTokens
			trace.EstimatedUsage.Reasoning += reasoningTokens
			for callIndex := range message.ToolCalls {
				call := &message.ToolCalls[callIndex]
				call.EstimatedTokens = estimateToolCallTokens(counter, *call)
				trace.EstimatedUsage.ToolCalls += call.EstimatedTokens
			}
		case "tool":
			message.EstimatedTokens = counter.Count(toolResultTokenText(*message))
			trace.EstimatedUsage.ToolResults += message.EstimatedTokens
		}
	}

	trace.EstimatedUsage.Total = trace.EstimatedUsage.SystemPrompt +
		trace.EstimatedUsage.User +
		trace.EstimatedUsage.Assistant +
		trace.EstimatedUsage.Reasoning +
		trace.EstimatedUsage.ToolCalls +
		trace.EstimatedUsage.ToolResults

	return trace
}

func estimateToolCallTokens(counter textTokenCounter, call StoredToolCall) int64 {
	if counter == nil {
		return 0
	}
	var total int64
	if call.Function != nil {
		total += counter.Count(call.Function.Name)
		total += counter.Count(call.Function.Arguments)
	}
	return total
}

func toolResultTokenText(message StoredMessage) string {
	if strings.TrimSpace(message.ContentText) != "" {
		return message.ContentText
	}
	if message.ContentJSON == nil {
		return ""
	}
	payload, err := json.Marshal(message.ContentJSON)
	if err != nil {
		return fmt.Sprint(message.ContentJSON)
	}
	return string(payload)
}

func newTokenEstimator(model string) textTokenCounter {
	encoder := cachedEncoderForModel(model)
	if encoder == nil {
		return nil
	}
	return &tokenEstimator{encoder: encoder}
}

func cachedEncoderForModel(model string) tokenEncoder {
	cacheKey := resolvedEncodingCacheKey(model)

	sharedEncoderCache.mu.RLock()
	if encoder, ok := sharedEncoderCache.encoders[cacheKey]; ok {
		sharedEncoderCache.mu.RUnlock()
		return encoder
	}
	sharedEncoderCache.mu.RUnlock()

	encoder, err := loadEncoder(model)
	if err != nil {
		return nil
	}

	sharedEncoderCache.mu.Lock()
	defer sharedEncoderCache.mu.Unlock()
	if existing, ok := sharedEncoderCache.encoders[cacheKey]; ok {
		return existing
	}
	sharedEncoderCache.encoders[cacheKey] = encoder
	return encoder
}

func loadEncoder(model string) (tokenEncoder, error) {
	model = strings.TrimSpace(model)
	if model != "" {
		encoder, err := tiktoken.EncodingForModel(model)
		if err == nil {
			return encoder, nil
		}
	}
	return tiktoken.GetEncoding(fallbackEncodingForModel(model))
}

func resolvedEncodingCacheKey(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return fallbackEncodingForModel("")
	}
	return model
}

func fallbackEncodingForModel(model string) string {
	model = strings.TrimSpace(strings.ToLower(model))
	switch {
	case model == "":
		return tiktoken.MODEL_O200K_BASE
	case strings.HasPrefix(model, "gpt-5"),
		strings.HasPrefix(model, "gpt-4.5"),
		strings.HasPrefix(model, "gpt-4.1"),
		strings.HasPrefix(model, "gpt-4o"):
		return tiktoken.MODEL_O200K_BASE
	case strings.HasPrefix(model, "gpt-4"),
		strings.HasPrefix(model, "gpt-3.5"),
		strings.HasPrefix(model, "text-embedding-3"),
		strings.HasPrefix(model, "text-embedding-ada-002"):
		return tiktoken.MODEL_CL100K_BASE
	case strings.Contains(model, "davinci"),
		strings.Contains(model, "cushman"),
		strings.Contains(model, "curie"),
		strings.Contains(model, "babbage"),
		strings.Contains(model, "ada"):
		return tiktoken.MODEL_R50K_BASE
	default:
		return tiktoken.MODEL_O200K_BASE
	}
}

func (te *tokenEstimator) Count(text string) int64 {
	if te == nil || te.encoder == nil || strings.TrimSpace(text) == "" {
		return 0
	}
	return int64(len(te.encoder.Encode(text, nil, nil)))
}

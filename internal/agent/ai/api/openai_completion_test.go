package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	protocol "oops/internal/agent/ai"
	"oops/internal/agent/ai/providers"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestOpenAICompletionStreamMergesToolCallArguments(t *testing.T) {
	index := 0
	chatModel := &chunkedStreamModel{chunks: []*schema.Message{
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			Index: &index,
			ID:    "call-1",
			Type:  "function",
			Function: schema.FunctionCall{
				Name:      "lookup",
				Arguments: `{"id":`,
			},
		}}},
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			Index: &index,
			ID:    "call-1",
			Type:  "function",
			Function: schema.FunctionCall{
				Name:      "lookup",
				Arguments: `1}`,
			},
		}}},
	}}
	streamFn, err := NewOpenAICompletionStream(chatModel, Options{})
	if err != nil {
		t.Fatalf("NewOpenAICompletionStream() error = %v", err)
	}

	stream, err := streamFn(context.Background(), protocol.StreamRequest{
		Context: protocol.Context{Messages: protocol.MessageList{
			protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("use lookup")}},
		}},
	})
	if err != nil {
		t.Fatalf("streamFn() error = %v", err)
	}

	var sawToolCallEnd bool
	for event := range stream.Events() {
		if event.Type != protocol.AssistantEventToolCallEnd {
			continue
		}
		sawToolCallEnd = true
		if event.ToolCall == nil || string(event.ToolCall.Arguments) != `{"id":1}` {
			t.Fatalf("toolcall_end = %#v", event.ToolCall)
		}
	}
	if !sawToolCallEnd {
		t.Fatal("missing toolcall_end event")
	}
	final, err := stream.Result(context.Background())
	if err != nil {
		t.Fatalf("stream.Result() error = %v", err)
	}
	calls := protocol.ToolCallsFromAssistant(*final)
	if len(calls) != 1 || string(calls[0].Arguments) != `{"id":1}` {
		t.Fatalf("final calls = %#v", calls)
	}
}

func TestOpenAICompletionStreamEmitsError(t *testing.T) {
	streamFn, err := NewOpenAICompletionStream(&chunkedStreamModel{err: errors.New("stream failed")}, Options{})
	if err != nil {
		t.Fatalf("NewOpenAICompletionStream() error = %v", err)
	}
	stream, err := streamFn(context.Background(), protocol.StreamRequest{})
	if err != nil {
		t.Fatalf("streamFn() error = %v", err)
	}
	event := <-stream.Events()
	if event.Type != protocol.AssistantEventError || event.Error == nil || event.Error.ErrorMessage != "stream failed" {
		t.Fatalf("event = %#v", event)
	}
}

func TestOpenAICompletionStreamEmitsOneErrorWhenModelReturnsNoChunks(t *testing.T) {
	streamFn, err := NewOpenAICompletionStream(&chunkedStreamModel{}, Options{})
	if err != nil {
		t.Fatalf("NewOpenAICompletionStream() error = %v", err)
	}
	stream, err := streamFn(context.Background(), protocol.StreamRequest{})
	if err != nil {
		t.Fatalf("streamFn() error = %v", err)
	}

	errorEvents := 0
	for event := range stream.Events() {
		if event.Type == protocol.AssistantEventError {
			errorEvents++
		}
	}
	if errorEvents != 1 {
		t.Fatalf("error event count = %d, want 1", errorEvents)
	}
}

func TestOpenAICompletionStreamTerminatesOnInvalidFinalMessage(t *testing.T) {
	index := 0
	streamFn, err := NewOpenAICompletionStream(&chunkedStreamModel{chunks: []*schema.Message{{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			Index: &index,
			Type:  "function",
			Function: schema.FunctionCall{
				Name:      "lookup",
				Arguments: `{}`,
			},
		}},
	}}}, Options{})
	if err != nil {
		t.Fatalf("NewOpenAICompletionStream() error = %v", err)
	}
	stream, err := streamFn(context.Background(), protocol.StreamRequest{})
	if err != nil {
		t.Fatalf("streamFn() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := stream.Result(ctx)
	if err != nil {
		t.Fatalf("Result() error = %v", err)
	}
	if result.StopReason != protocol.StopReasonError {
		t.Fatalf("result.StopReason = %q, want error", result.StopReason)
	}
	if !strings.Contains(result.ErrorMessage, "tool call content requires id") {
		t.Fatalf("result.ErrorMessage = %q", result.ErrorMessage)
	}
}

func TestModelMessageRoundTripPreservesToolUsageAndReasoning(t *testing.T) {
	reasoning := 7
	message := protocol.AssistantMessage{
		Content: protocol.ContentList{
			protocol.NewThinkingContent("considering"),
			protocol.NewTextContent("using tool"),
			protocol.NewToolCallContent("call_1", "lookup", json.RawMessage(`{"query":"pods"}`)),
		},
		Usage:      protocol.Usage{Input: 11, Output: 13, CacheRead: 5, Reasoning: &reasoning, TotalTokens: 24},
		StopReason: protocol.StopReasonToolUse,
	}

	modelMessage, err := ToModelMessage(message)
	if err != nil {
		t.Fatalf("ToModelMessage() error = %v", err)
	}
	if modelMessage.ResponseMeta == nil || modelMessage.ResponseMeta.FinishReason != "tool_calls" {
		t.Fatalf("ResponseMeta = %#v", modelMessage.ResponseMeta)
	}
	converted, err := FromModelMessage(modelMessage)
	if err != nil {
		t.Fatalf("FromModelMessage() error = %v", err)
	}
	assistant, ok := converted.(protocol.AssistantMessage)
	if !ok || assistant.StopReason != protocol.StopReasonToolUse || len(assistant.Content) != 3 {
		t.Fatalf("converted = %#v", converted)
	}
}

func TestFromModelMessageRejectsSystemRole(t *testing.T) {
	_, err := FromModelMessage(schema.SystemMessage("system"))
	if !errors.Is(err, ErrSystemBoundary) {
		t.Fatalf("FromModelMessage(system) error = %v, want ErrSystemBoundary", err)
	}
}

func TestModelUserMultimodalRoundTrip(t *testing.T) {
	imageData := "aW1hZ2U="
	message := &schema.Message{
		Role: schema.User,
		UserInputMultiContent: []schema.MessageInputPart{
			{Type: schema.ChatMessagePartTypeText, Text: "look"},
			{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{
				MessagePartCommon: schema.MessagePartCommon{Base64Data: &imageData, MIMEType: "image/png"},
				Detail:            schema.ImageURLDetailHigh,
			}},
		},
	}
	converted, err := FromModelMessage(message)
	if err != nil {
		t.Fatalf("FromModelMessage() error = %v", err)
	}
	back, err := ToModelMessage(converted)
	if err != nil {
		t.Fatalf("ToModelMessage() error = %v", err)
	}
	if back.Role != schema.User || len(back.UserInputMultiContent) != 2 {
		t.Fatalf("roundtrip user message = %#v", back)
	}
	if back.UserInputMultiContent[1].Image == nil || back.UserInputMultiContent[1].Image.MIMEType != "image/png" {
		t.Fatalf("roundtrip image = %#v", back.UserInputMultiContent[1].Image)
	}
}

func TestModelToolResultRoundTrip(t *testing.T) {
	message := protocol.ToolResultMessage{
		ToolCallID: "call_1",
		ToolName:   "lookup",
		Content:    protocol.ContentList{protocol.NewTextContent("found")},
	}
	modelMessage, err := ToModelMessage(message)
	if err != nil {
		t.Fatalf("ToModelMessage() error = %v", err)
	}
	if modelMessage.Role != schema.Tool || modelMessage.ToolCallID != "call_1" || modelMessage.ToolName != "lookup" {
		t.Fatalf("tool message = %#v", modelMessage)
	}
	converted, err := FromModelMessage(modelMessage)
	if err != nil {
		t.Fatalf("FromModelMessage() error = %v", err)
	}
	toolResult, ok := converted.(protocol.ToolResultMessage)
	if !ok || toolResult.ToolName != "lookup" {
		t.Fatalf("converted = %#v", converted)
	}
}

func TestModelToolResultMultimodalUsesInputParts(t *testing.T) {
	message := protocol.ToolResultMessage{
		ToolCallID: "call_1",
		ToolName:   "lookup",
		Content: protocol.ContentList{
			protocol.NewTextContent("found"),
			protocol.ImageContent{Type: protocol.ContentTypeImage, Data: "aW1hZ2U=", MIMEType: "image/png", Detail: "high"},
		},
	}
	modelMessage, err := ToModelMessage(message)
	if err != nil {
		t.Fatalf("ToModelMessage() error = %v", err)
	}
	if len(modelMessage.UserInputMultiContent) != 2 {
		t.Fatalf("UserInputMultiContent = %#v", modelMessage.UserInputMultiContent)
	}
	converted, err := FromModelMessage(modelMessage)
	if err != nil {
		t.Fatalf("FromModelMessage() error = %v", err)
	}
	toolResult, ok := converted.(protocol.ToolResultMessage)
	if !ok || len(toolResult.Content) != 2 {
		t.Fatalf("converted = %#v", converted)
	}
	image, ok := toolResult.Content[1].(protocol.ImageContent)
	if !ok || image.Data != "aW1hZ2U=" || image.MIMEType != "image/png" || image.Detail != "high" {
		t.Fatalf("roundtrip image = %#v", toolResult.Content[1])
	}
}

func TestFromModelToolMessageRejectsInvalidMultimodalContent(t *testing.T) {
	_, err := FromModelMessage(&schema.Message{
		Role:       schema.Tool,
		ToolCallID: "call_1",
		ToolName:   "lookup",
		UserInputMultiContent: []schema.MessageInputPart{{
			Type: schema.ChatMessagePartTypeImageURL,
		}},
	})
	if err == nil {
		t.Fatal("FromModelMessage() error = nil, want invalid image error")
	}
}

func TestRawToolArgumentsNormalizesOmittedArguments(t *testing.T) {
	arguments, err := rawToolArguments(" \t")
	if err != nil {
		t.Fatalf("rawToolArguments() error = %v", err)
	}
	if string(arguments) != "{}" {
		t.Fatalf("arguments = %s, want {}", arguments)
	}
}

func TestOpenAICompletionStreamReplaysAssistantThinking(t *testing.T) {
	chatModel := &chunkedStreamModel{chunks: []*schema.Message{{Role: schema.Assistant, Content: "ok"}}}
	streamFn, err := NewOpenAICompletionStream(chatModel, Options{})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := streamFn(context.Background(), protocol.StreamRequest{Context: protocol.Context{Messages: protocol.MessageList{
		protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("hello")}},
		protocol.AssistantMessage{Content: protocol.ContentList{protocol.NewThinkingContent("private reasoning"), protocol.NewTextContent("visible answer")}, StopReason: protocol.StopReasonStop},
		protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("next")}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	drainStream(t, stream)
	if len(chatModel.messages) != 3 {
		t.Fatalf("messages = %#v, want 3", chatModel.messages)
	}
	assistant := chatModel.messages[1]
	if assistant.Role != schema.Assistant || assistant.Content != "visible answer" || assistant.ReasoningContent != "private reasoning" {
		t.Fatalf("assistant = %#v", assistant)
	}
}

func TestOpenAICompletionStreamDeepSeekRequestPayload(t *testing.T) {
	tests := []struct {
		name                 string
		provider             string
		reasoning            string
		wantReasoningContent bool
		wantThinking         string
		wantEffort           string
	}{
		{
			name:                 "deepseek enables thinking and maps xhigh",
			provider:             providers.DeepSeekID,
			reasoning:            ThinkingLevelXHigh,
			wantReasoningContent: true,
			wantThinking:         "enabled",
			wantEffort:           "max",
		},
		{
			name:                 "deepseek maps low to high",
			provider:             providers.DeepSeekID,
			reasoning:            ThinkingLevelLow,
			wantReasoningContent: true,
			wantThinking:         "enabled",
			wantEffort:           "high",
		},
		{
			name:                 "deepseek disables thinking when unset",
			provider:             providers.DeepSeekID,
			wantReasoningContent: true,
			wantThinking:         "disabled",
		},
		{
			name:       "openai compatible keeps standard payload",
			provider:   protocol.DefaultProviderID,
			reasoning:  ThinkingLevelXHigh,
			wantEffort: "high",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payloads := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				payload, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request body: %v", err)
				}
				payloads <- payload
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"2\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
			}))
			defer server.Close()

			chatModel, err := NewOpenAICompletionModel(context.Background(), OpenAICompletionConfig{
				Model:   "test-model",
				APIKey:  "test-key",
				BaseURL: server.URL,
			})
			if err != nil {
				t.Fatalf("NewOpenAICompletionModel() error = %v", err)
			}
			streamFn, err := NewOpenAICompletionStream(chatModel, Options{})
			if err != nil {
				t.Fatalf("NewOpenAICompletionStream() error = %v", err)
			}
			stream, err := streamFn(context.Background(), protocol.StreamRequest{
				Provider:  test.provider,
				Model:     "test-model",
				Reasoning: test.reasoning,
				Context: protocol.Context{Messages: protocol.MessageList{
					protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("find it")}},
					protocol.AssistantMessage{
						Provider: test.provider,
						Model:    "test-model",
						Content: protocol.ContentList{
							protocol.NewToolCallContent("call_1", "lookup", json.RawMessage(`{"query":"pods"}`)),
						},
						StopReason: protocol.StopReasonToolUse,
					},
					protocol.ToolResultMessage{
						ToolCallID: "call_1",
						ToolName:   "lookup",
						Content:    protocol.ContentList{protocol.NewTextContent("found")},
					},
					protocol.AssistantMessage{
						Provider: test.provider,
						Model:    "test-model",
						Content: protocol.ContentList{
							protocol.NewThinkingContent("kept reasoning"),
							protocol.NewTextContent("previous result"),
						},
						StopReason: protocol.StopReasonStop,
					},
					protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("continue")}},
				}},
			})
			if err != nil {
				t.Fatalf("streamFn() error = %v", err)
			}
			drainStream(t, stream)

			var payload map[string]json.RawMessage
			if err := json.Unmarshal(<-payloads, &payload); err != nil {
				t.Fatalf("decode request payload: %v", err)
			}
			assertDeepSeekRequestPayload(t, payload, test.wantReasoningContent, test.wantThinking, test.wantEffort)
		})
	}
}

func assertDeepSeekRequestPayload(t *testing.T, payload map[string]json.RawMessage, wantReasoningContent bool, wantThinking, wantEffort string) {
	t.Helper()
	if wantThinking == "" {
		if _, ok := payload["thinking"]; ok {
			t.Fatalf("thinking = %s", payload["thinking"])
		}
	} else {
		var thinking struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(payload["thinking"], &thinking); err != nil {
			t.Fatalf("decode thinking = %v", err)
		}
		if thinking.Type != wantThinking {
			t.Fatalf("thinking.type = %q, want %q", thinking.Type, wantThinking)
		}
	}
	if wantEffort == "" {
		if _, ok := payload["reasoning_effort"]; ok {
			t.Fatalf("reasoning_effort = %s", payload["reasoning_effort"])
		}
	} else {
		var effort string
		if err := json.Unmarshal(payload["reasoning_effort"], &effort); err != nil {
			t.Fatalf("decode reasoning_effort = %v", err)
		}
		if effort != wantEffort {
			t.Fatalf("reasoning_effort = %q, want %q", effort, wantEffort)
		}
	}

	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(payload["messages"], &messages); err != nil {
		t.Fatalf("decode messages = %v", err)
	}
	var assistants []map[string]json.RawMessage
	for _, message := range messages {
		var role string
		if err := json.Unmarshal(message["role"], &role); err == nil && role == "assistant" {
			assistants = append(assistants, message)
		}
	}
	if len(assistants) != 2 {
		t.Fatalf("assistant messages = %#v", assistants)
	}
	_, foundReasoningContent := assistants[0]["reasoning_content"]
	if foundReasoningContent != wantReasoningContent {
		t.Fatalf("tool-call reasoning_content present = %v, want %v", foundReasoningContent, wantReasoningContent)
	}
	if wantReasoningContent {
		var reasoningContent string
		if err := json.Unmarshal(assistants[0]["reasoning_content"], &reasoningContent); err != nil {
			t.Fatalf("decode tool-call reasoning_content = %v", err)
		}
		if reasoningContent != "" {
			t.Fatalf("tool-call reasoning_content = %q, want empty", reasoningContent)
		}
	}
	var retainedReasoning string
	if err := json.Unmarshal(assistants[1]["reasoning_content"], &retainedReasoning); err != nil {
		t.Fatalf("decode retained reasoning_content = %v", err)
	}
	if retainedReasoning != "kept reasoning" {
		t.Fatalf("retained reasoning_content = %q", retainedReasoning)
	}
}

func TestOpenAICompletionStreamSkipsUnreplayableAssistantHistory(t *testing.T) {
	chatModel := &chunkedStreamModel{chunks: []*schema.Message{{Role: schema.Assistant, Content: "ok"}}}
	streamFn, err := NewOpenAICompletionStream(chatModel, Options{})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := streamFn(context.Background(), protocol.StreamRequest{Context: protocol.Context{Messages: protocol.MessageList{
		protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("hello")}},
		protocol.AssistantMessage{Content: protocol.ContentList{protocol.NewTextContent("failed")}, StopReason: protocol.StopReasonError, ErrorMessage: "failed"},
		protocol.AssistantMessage{Content: protocol.ContentList{protocol.NewThinkingContent("only reasoning")}, StopReason: protocol.StopReasonStop},
		protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("next")}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	drainStream(t, stream)
	if len(chatModel.messages) != 2 {
		t.Fatalf("messages = %#v, want 2", chatModel.messages)
	}
}

func TestOpenAICompletionStreamCompletesDanglingToolCallHistory(t *testing.T) {
	chatModel := &chunkedStreamModel{chunks: []*schema.Message{{Role: schema.Assistant, Content: "ok"}}}
	streamFn, err := NewOpenAICompletionStream(chatModel, Options{})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := streamFn(context.Background(), protocol.StreamRequest{Context: protocol.Context{Messages: protocol.MessageList{
		protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("hello")}},
		protocol.AssistantMessage{Content: protocol.ContentList{protocol.NewToolCallContent("call_1", "read", json.RawMessage(`{"path":"README.md"}`))}, StopReason: protocol.StopReasonToolUse},
		protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("next")}},
	}}})
	if err != nil {
		t.Fatalf("streamFn() error = %v", err)
	}
	drainStream(t, stream)
	if len(chatModel.messages) != 4 {
		t.Fatalf("messages = %#v, want user, assistant, synthetic tool result, user", chatModel.messages)
	}
	synthetic := chatModel.messages[2]
	if synthetic.Role != schema.Tool || synthetic.ToolCallID != "call_1" || synthetic.ToolName != "read" || synthetic.Content != missingToolResultText {
		t.Fatalf("synthetic tool result = %#v", synthetic)
	}
}

func TestOpenAICompletionStreamSetsAssistantModelIdentity(t *testing.T) {
	streamFn, err := NewOpenAICompletionStream(&chunkedStreamModel{chunks: []*schema.Message{{Role: schema.Assistant, Content: "ok"}}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := streamFn(context.Background(), protocol.StreamRequest{
		Provider: "openai-compatible",
		Model:    "model-id",
	})
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events() {
	}
	final, err := stream.Result(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if final.Provider != "openai-compatible" || final.Model != "model-id" {
		t.Fatalf("assistant identity = provider:%q model:%q", final.Provider, final.Model)
	}
}

func TestOpenAICompletionStreamBindsToolsPerRequest(t *testing.T) {
	chatModel := &chunkedStreamModel{chunks: []*schema.Message{{Role: schema.Assistant, Content: "ok"}}}
	streamFn, err := NewOpenAICompletionStream(chatModel, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"read", "write"} {
		stream, err := streamFn(context.Background(), protocol.StreamRequest{Context: protocol.Context{
			Messages: protocol.MessageList{protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent(name)}}},
			Tools:    []protocol.ToolDefinition{{Name: name, Parameters: json.RawMessage(`{"type":"object"}`)}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		drainStream(t, stream)
	}
	if len(chatModel.toolBinds) != 2 || chatModel.toolBinds[0][0] != "read" || chatModel.toolBinds[1][0] != "write" {
		t.Fatalf("tool binds = %#v", chatModel.toolBinds)
	}
}

func TestOpenAICompletionStreamAppliesBaseOptions(t *testing.T) {
	temperature := float32(0.2)
	chatModel := &chunkedStreamModel{chunks: []*schema.Message{{Role: schema.Assistant, Content: "ok"}}}
	streamFn, err := NewOpenAICompletionStream(chatModel, Options{
		ContextWindow: 5098,
		MaxTokens:     2000,
		Temperature:   &temperature,
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := streamFn(context.Background(), protocol.StreamRequest{Context: protocol.Context{Messages: protocol.MessageList{
		protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("12345678")}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	drainStream(t, stream)
	options := model.GetCommonOptions(nil, chatModel.options...)
	if options.MaxTokens == nil || *options.MaxTokens != 1000 {
		t.Fatalf("MaxTokens = %#v, want 1000", options.MaxTokens)
	}
	if options.Temperature == nil || *options.Temperature != temperature {
		t.Fatalf("Temperature = %#v, want %f", options.Temperature, temperature)
	}
}

func TestOpenAIReasoningEffort(t *testing.T) {
	effort, ok := openAIReasoningEffort(ThinkingLevelXHigh)
	if !ok || effort != openai.ReasoningEffortLevelHigh {
		t.Fatalf("openAIReasoningEffort(xhigh) = (%q, %v)", effort, ok)
	}
	if _, ok := openAIReasoningEffort("off"); ok {
		t.Fatal("openAIReasoningEffort(off) selected an effort")
	}
}

type chunkedStreamModel struct {
	chunks    []*schema.Message
	err       error
	messages  []*schema.Message
	options   []model.Option
	toolBinds [][]string
}

func (m *chunkedStreamModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	if m.err != nil {
		return nil, m.err
	}
	return schema.ConcatMessages(m.chunks)
}

func (m *chunkedStreamModel) Stream(_ context.Context, messages []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.messages = messages
	m.options = append([]model.Option(nil), options...)
	if m.err != nil {
		reader, writer := schema.Pipe[*schema.Message](1)
		writer.Send(nil, m.err)
		writer.Close()
		return reader, nil
	}
	return schema.StreamReaderFromArray(m.chunks), nil
}

func (m *chunkedStreamModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	names := make([]string, 0, len(infos))
	for _, info := range infos {
		if info != nil {
			names = append(names, info.Name)
		}
	}
	m.toolBinds = append(m.toolBinds, names)
	return m, nil
}

func drainStream(t *testing.T, stream *protocol.AssistantMessageEventStream) {
	t.Helper()
	for range stream.Events() {
	}
	if _, err := stream.Result(context.Background()); err != nil {
		t.Fatalf("stream.Result() error = %v", err)
	}
}

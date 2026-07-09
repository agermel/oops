package provider

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"oops/internal/llm/ai/protocol"
	coreagent "oops/internal/llm/core/agent"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestEinoStreamFnHandlesSplitToolCallArguments(t *testing.T) {
	index := 0
	chatModel := &chunkedStreamModel{
		chunks: []*schema.Message{
			{
				Role: schema.Assistant,
				ToolCalls: []schema.ToolCall{{
					Index: &index,
					ID:    "call-1",
					Type:  "function",
					Function: schema.FunctionCall{
						Name:      "lookup",
						Arguments: `{"id":`,
					},
				}},
			},
			{
				Role: schema.Assistant,
				ToolCalls: []schema.ToolCall{{
					Index: &index,
					ID:    "call-1",
					Type:  "function",
					Function: schema.FunctionCall{
						Name:      "lookup",
						Arguments: `1}`,
					},
				}},
			},
		},
	}
	streamFn, err := NewEinoStreamFn(context.Background(), chatModel, nil)
	if err != nil {
		t.Fatalf("NewEinoStreamFn() error = %v", err)
	}

	stream, err := streamFn(context.Background(), coreagent.StreamRequest{
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
		if event.ToolCall == nil {
			t.Fatal("toolcall_end missing tool call")
		}
		if got := string(event.ToolCall.Arguments); got != `{"id":1}` {
			t.Fatalf("tool call arguments = %q, want merged json", got)
		}
	}
	if !sawToolCallEnd {
		t.Fatal("missing toolcall_end event")
	}
	final, err := stream.Result(context.Background())
	if err != nil {
		t.Fatalf("stream.Result() error = %v", err)
	}
	calls := toolCallsFromAssistant(*final)
	if len(calls) != 1 {
		t.Fatalf("tool calls = %#v, want one", calls)
	}
	if string(calls[0].Arguments) != `{"id":1}` {
		t.Fatalf("final arguments = %s, want merged json", calls[0].Arguments)
	}
}

func TestEinoStreamFnEmitsErrorEventForStreamError(t *testing.T) {
	streamFn, err := NewEinoStreamFn(context.Background(), &chunkedStreamModel{err: errors.New("stream failed")}, nil)
	if err != nil {
		t.Fatalf("NewEinoStreamFn() error = %v", err)
	}
	stream, err := streamFn(context.Background(), coreagent.StreamRequest{})
	if err != nil {
		t.Fatalf("streamFn() error = %v", err)
	}
	event := <-stream.Events()
	if event.Type != protocol.AssistantEventError {
		t.Fatalf("event type = %s, want error", event.Type)
	}
	if event.Error == nil || event.Error.ErrorMessage != "stream failed" {
		t.Fatalf("error event = %#v", event)
	}
}

func TestEinoStreamFnReplaysAssistantThinkingWithoutOutputParts(t *testing.T) {
	chatModel := &chunkedStreamModel{
		chunks: []*schema.Message{{Role: schema.Assistant, Content: "ok"}},
	}
	streamFn, err := NewEinoStreamFn(context.Background(), chatModel, nil)
	if err != nil {
		t.Fatalf("NewEinoStreamFn() error = %v", err)
	}

	stream, err := streamFn(context.Background(), coreagent.StreamRequest{
		Context: protocol.Context{Messages: protocol.MessageList{
			protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("hello")}},
			protocol.AssistantMessage{
				Content: protocol.ContentList{
					protocol.NewThinkingContent("private reasoning"),
					protocol.NewTextContent("visible answer"),
				},
				StopReason: protocol.StopReasonStop,
			},
			protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("next")}},
		}},
	})
	if err != nil {
		t.Fatalf("streamFn() error = %v", err)
	}
	drainStream(t, stream)

	if len(chatModel.messages) != 3 {
		t.Fatalf("messages = %#v, want 3", chatModel.messages)
	}
	assistant := chatModel.messages[1]
	if assistant.Role != schema.Assistant {
		t.Fatalf("assistant role = %q", assistant.Role)
	}
	if assistant.Content != "visible answer" {
		t.Fatalf("assistant content = %q, want visible answer", assistant.Content)
	}
	if assistant.ReasoningContent != "private reasoning" {
		t.Fatalf("ReasoningContent = %q, want private reasoning", assistant.ReasoningContent)
	}
	if len(assistant.AssistantGenMultiContent) != 0 {
		t.Fatalf("AssistantGenMultiContent = %#v, want empty", assistant.AssistantGenMultiContent)
	}
}

func TestEinoStreamFnSkipsUnreplayableAssistantHistory(t *testing.T) {
	chatModel := &chunkedStreamModel{
		chunks: []*schema.Message{{Role: schema.Assistant, Content: "ok"}},
	}
	streamFn, err := NewEinoStreamFn(context.Background(), chatModel, nil)
	if err != nil {
		t.Fatalf("NewEinoStreamFn() error = %v", err)
	}

	stream, err := streamFn(context.Background(), coreagent.StreamRequest{
		Context: protocol.Context{Messages: protocol.MessageList{
			protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("hello")}},
			protocol.AssistantMessage{
				Content:      protocol.ContentList{protocol.NewTextContent("failed")},
				StopReason:   protocol.StopReasonError,
				ErrorMessage: "failed",
			},
			protocol.AssistantMessage{
				Content:    protocol.ContentList{protocol.NewThinkingContent("only reasoning")},
				StopReason: protocol.StopReasonStop,
			},
			protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("next")}},
		}},
	})
	if err != nil {
		t.Fatalf("streamFn() error = %v", err)
	}
	drainStream(t, stream)

	if len(chatModel.messages) != 2 {
		t.Fatalf("messages = %#v, want 2", chatModel.messages)
	}
	for _, message := range chatModel.messages {
		if message.Role == schema.Assistant {
			t.Fatalf("unexpected assistant message replayed: %#v", message)
		}
	}
}

func TestEinoStreamFnRejectsDanglingAssistantToolCallHistory(t *testing.T) {
	chatModel := &chunkedStreamModel{
		chunks: []*schema.Message{{Role: schema.Assistant, Content: "ok"}},
	}
	streamFn, err := NewEinoStreamFn(context.Background(), chatModel, nil)
	if err != nil {
		t.Fatalf("NewEinoStreamFn() error = %v", err)
	}

	_, err = streamFn(context.Background(), coreagent.StreamRequest{
		Context: protocol.Context{Messages: protocol.MessageList{
			protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("hello")}},
			protocol.AssistantMessage{
				Content: protocol.ContentList{
					protocol.NewToolCallContent("call_1", "read", json.RawMessage(`{"path":"README.md"}`)),
				},
				StopReason: protocol.StopReasonToolUse,
			},
			protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("next")}},
		}},
	})
	if err == nil {
		t.Fatal("streamFn() nil error, want dangling tool call error")
	}
	if len(chatModel.messages) != 0 {
		t.Fatalf("model received messages = %#v, want none", chatModel.messages)
	}
}

type chunkedStreamModel struct {
	chunks   []*schema.Message
	err      error
	messages []*schema.Message
}

func (m *chunkedStreamModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	if m.err != nil {
		return nil, m.err
	}
	return schema.ConcatMessages(m.chunks)
}

func (m *chunkedStreamModel) Stream(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.messages = messages
	if m.err != nil {
		reader, writer := schema.Pipe[*schema.Message](1)
		writer.Send(nil, m.err)
		writer.Close()
		return reader, nil
	}
	return schema.StreamReaderFromArray(m.chunks), nil
}

func (m *chunkedStreamModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
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

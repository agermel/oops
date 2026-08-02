package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"strings"

	protocol "oops/internal/agent/ai"
	"oops/internal/agent/ai/providers"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	einojsonschema "github.com/eino-contrib/jsonschema"
)

// 清理掉来着python的错误，可疑，待清理
var providerPydanticTraceRE = regexp.MustCompile(`\n For further information visit https?://[^\s]+`)

// 哨兵错误 1.创建流时缺少聊天模型实例 2.转换 Eino 消息时收到空消息 3.系统提示属于 Context.SystemPrompt，消息转换函数用它标记协议边界
var (
	ErrMissingModel   = errors.New("openai completion requires chat model")
	ErrNilMessage     = errors.New("model message is nil")
	ErrSystemBoundary = errors.New("system message belongs to context conversion")
)

// OpenAICompletionConfig 连接配置
type OpenAICompletionConfig struct {
	Model   string
	APIKey  string
	BaseURL string
}

// NewOpenAICompletionModel 创建新模型，使用 Eino 包装，底层使用 OpenAICompletion 协议
func NewOpenAICompletionModel(ctx context.Context, cfg OpenAICompletionConfig) (model.ToolCallingChatModel, error) {
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		Model:   cfg.Model,
		APIKey:  cfg.APIKey,
		BaseURL: cfg.BaseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("create openai completion model: %w", err)
	}
	return chatModel, nil
}

// OpenAICompletionFactory 构建模型 -> 返回模型的流式调用函数
func OpenAICompletionFactory(ctx context.Context, cfg protocol.ProviderConfig) (protocol.StreamFunc, error) {
	apiKey := cfg.APIKey
	if hasAuthorizationHeader(cfg.Headers) {
		apiKey = ""
	}
	chatModel, err := NewOpenAICompletionModel(ctx, OpenAICompletionConfig{
		Model:   cfg.Model,
		APIKey:  apiKey,
		BaseURL: cfg.BaseURL,
	})
	if err != nil {
		return nil, err
	}
	return NewOpenAICompletionStream(chatModel, Options{
		ContextWindow: cfg.ContextWindow,
		MaxTokens:     cfg.MaxTokens,
		Temperature:   cfg.Temperature,
		Headers:       cfg.Headers,
	})
}

// NewOpenAICompletionStream 构建模型的流式调用函数
func NewOpenAICompletionStream(chatModel model.ToolCallingChatModel, options Options) (protocol.StreamFunc, error) {
	if chatModel == nil {
		return nil, ErrMissingModel
	}
	return func(runCtx context.Context, req protocol.StreamRequest) (*protocol.AssistantMessageEventStream, error) {
		requestContext := req.Context
		// 处理好历史信息（补全toolresult，针对跨模型之间的上下文标准差异进行格式处理）
		requestContext.Messages = TransformMessages(req.Context.Messages, TransformMessagesOptions{
			Provider:       req.Provider,
			Model:          req.Model,
			SupportsImages: true,
		})
		// 转换 Context 的消息 -> openai
		messages, err := messagesFromContext(requestContext)
		if err != nil {
			return nil, err
		}
		// 转换工具列表
		toolInfos, err := toolInfosFromDefinitions(req.Context.Tools)
		if err != nil {
			return nil, err
		}
		// 将 tool 带入 model
		modelForRun := chatModel
		if len(toolInfos) > 0 {
			bound, err := chatModel.WithTools(cloneToolInfos(toolInfos))
			if err != nil {
				return nil, err
			}
			modelForRun = bound
		}

		// 流式请求 model
		reader, err := modelForRun.Stream(runCtx, messages, openAICompletionOptions(options, requestContext, req.Reasoning, req.Provider, req.MaxTokens)...)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || runCtx.Err() != nil {
				stream := protocol.NewAssistantMessageEventStream(1)
				pushAssistantError(stream, err, protocol.StopReasonAborted, req.Provider, req.Model)
				return stream, nil
			}
			return nil, err
		}
		// 创建内部协议的助手消息事件流式响应，通道的缓冲容量为 16。
		stream := protocol.NewAssistantMessageEventStream(16)
		// 将 eino 模型流转换为内部协议流
		go drainMessageStream(runCtx, reader, stream, req.Provider, req.Model)
		return stream, nil
	}, nil
}

// 组装配置 Options -> Eino Option
func openAICompletionOptions(options Options, ctx protocol.Context, reasoning, providerID string, requestMaxTokens int) []model.Option {
	if requestMaxTokens > 0 {
		if options.MaxTokens > 0 {
			options.MaxTokens = min(options.MaxTokens, requestMaxTokens)
		} else {
			options.MaxTokens = requestMaxTokens
		}
	}
	effort, ok := openAIReasoningEffort(reasoning)
	callOptions := BuildBaseOptions(options, ctx)
	if ok {
		callOptions = BuildBaseOptions(Options{Temperature: options.Temperature}, ctx)
		if maxTokens, maxTokensOK := MaxTokensForContext(options, ctx); maxTokensOK {
			callOptions = append(callOptions, openai.WithMaxCompletionTokens(maxTokens))
		}
		callOptions = append(callOptions, openai.WithReasoningEffort(effort))
	}
	if len(options.Headers) > 0 {
		callOptions = append(callOptions, openai.WithExtraHeader(maps.Clone(options.Headers)))
	}
	if providerID == providers.DeepSeekID {
		callOptions = append(callOptions, openai.WithRequestPayloadModifier(func(_ context.Context, _ []*schema.Message, rawBody []byte) ([]byte, error) {
			return modifyDeepSeekRequestPayload(rawBody, reasoning)
		}))
	}
	return callOptions
}

func hasAuthorizationHeader(headers map[string]string) bool {
	for name := range headers {
		if strings.EqualFold(name, "Authorization") {
			return true
		}
	}
	return false
}

// 针对 deepseek 进行 payload 调整
func modifyDeepSeekRequestPayload(rawBody []byte, reasoning string) ([]byte, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		return nil, fmt.Errorf("decode chat completion payload: %w", err)
	}
	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" || reasoning == "off" {
		payload["thinking"] = json.RawMessage(`{"type":"disabled"}`)
	} else {
		payload["thinking"] = json.RawMessage(`{"type":"enabled"}`)
		if reasoning == ThinkingLevelXHigh {
			payload["reasoning_effort"] = json.RawMessage(`"max"`)
		} else {
			payload["reasoning_effort"] = json.RawMessage(`"high"`)
		}
	}
	if rawMessages, ok := payload["messages"]; ok {
		var messages []map[string]json.RawMessage
		if err := json.Unmarshal(rawMessages, &messages); err != nil {
			return nil, fmt.Errorf("decode chat completion messages: %w", err)
		}
		for _, message := range messages {
			var role string
			if err := json.Unmarshal(message["role"], &role); err != nil || role != string(schema.Assistant) {
				continue
			}
			if _, ok := message["reasoning_content"]; ok {
				continue
			}
			message["reasoning_content"] = json.RawMessage(`""`)
		}
		encodedMessages, err := json.Marshal(messages)
		if err != nil {
			return nil, fmt.Errorf("encode chat completion messages: %w", err)
		}
		payload["messages"] = encodedMessages
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode chat completion payload: %w", err)
	}
	return encodedPayload, nil
}

func openAIReasoningEffort(reasoning string) (openai.ReasoningEffortLevel, bool) {
	switch ClampReasoning(reasoning) {
	case ThinkingLevelMinimal, ThinkingLevelLow:
		return openai.ReasoningEffortLevelLow, true
	case ThinkingLevelMedium:
		return openai.ReasoningEffortLevelMedium, true
	case ThinkingLevelHigh:
		return openai.ReasoningEffortLevelHigh, true
	default:
		return "", false
	}
}

func messagesFromContext(ctx protocol.Context) ([]*schema.Message, error) {
	messages := make([]*schema.Message, 0, len(ctx.Messages)+1)
	if ctx.SystemPrompt != "" {
		messages = append(messages, schema.SystemMessage(ctx.SystemPrompt))
	}
	for _, message := range ctx.Messages {
		converted, err := ToModelMessage(message)
		if err != nil {
			return nil, err
		}
		if isEmptyAssistantForProvider(converted) {
			continue
		}
		messages = append(messages, converted)
	}
	return messages, nil
}

func isEmptyAssistantForProvider(message *schema.Message) bool {
	if message == nil || message.Role != schema.Assistant {
		return false
	}
	return message.Content == "" &&
		len(message.ToolCalls) == 0 &&
		len(message.AssistantGenMultiContent) == 0 &&
		len(message.MultiContent) == 0
}

func drainMessageStream(ctx context.Context, reader *schema.StreamReader[*schema.Message], out *protocol.AssistantMessageEventStream, provider, modelID string) {
	defer reader.Close()
	started := false
	var chunks []*schema.Message
	var text, thinking string
	toolCalls := map[int]*streamingToolCall{}
	var toolOrder []int
	for {
		message, err := reader.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			reason := protocol.StopReasonError
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
				reason = protocol.StopReasonAborted
			}
			pushAssistantError(out, err, reason, provider, modelID)
			return
		}
		if message == nil {
			continue
		}
		chunks = append(chunks, message)
		if !started {
			partial := streamingPartial(thinking, text, provider, modelID)
			if err := out.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventStart, Partial: &partial}); err != nil {
				return
			}
			started = true
		}
		text, thinking = pushTextChunkUpdates(out, message, text, thinking, provider, modelID)
		pushToolCallChunkUpdates(out, message, text, thinking, provider, modelID, toolCalls, &toolOrder)
	}
	if err := ctx.Err(); err != nil {
		pushAssistantError(out, err, protocol.StopReasonAborted, provider, modelID)
		return
	}
	if len(chunks) == 0 {
		pushAssistantError(out, protocol.ErrStreamEndedWithoutResult, protocol.StopReasonError, provider, modelID)
		return
	}
	final, err := partialAssistantMessage(chunks, provider, modelID)
	if err != nil {
		pushAssistantError(out, err, protocol.StopReasonError, provider, modelID)
		return
	}
	if err := final.Validate(); err != nil {
		pushAssistantError(out, err, protocol.StopReasonError, provider, modelID)
		return
	}
	if !started {
		if err := out.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventStart, Partial: &final}); err != nil {
			return
		}
	}
	if thinking != "" {
		index := thinkingContentIndex(final)
		_ = out.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventThinkingEnd, ContentIndex: &index, Content: thinking, Partial: &final})
	}
	if text != "" {
		index := textContentIndex(final)
		_ = out.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventTextEnd, ContentIndex: &index, Content: text, Partial: &final})
	}
	pushFinalToolCalls(out, final)
	if final.StopReason == protocol.StopReasonError || final.StopReason == protocol.StopReasonAborted {
		if final.ErrorMessage == "" {
			if final.StopReason == protocol.StopReasonAborted {
				final.ErrorMessage = "request was aborted"
			} else {
				final.ErrorMessage = "provider returned an error stop reason"
			}
		}
		_ = out.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventError, Reason: final.StopReason, Error: &final})
		return
	}
	_ = out.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventDone, Reason: final.StopReason, Message: &final})
}

type streamingToolCall struct {
	id        string
	name      string
	arguments string
	started   bool
}

func pushToolCallChunkUpdates(out *protocol.AssistantMessageEventStream, message *schema.Message, text, thinking, provider, modelID string, calls map[int]*streamingToolCall, order *[]int) {
	for position, chunk := range message.ToolCalls {
		key := position
		if chunk.Index != nil {
			key = *chunk.Index
		}
		call, ok := calls[key]
		if !ok {
			call = &streamingToolCall{}
			calls[key] = call
			*order = append(*order, key)
		}
		if chunk.ID != "" {
			call.id = chunk.ID
		}
		if chunk.Function.Name != "" {
			call.name = chunk.Function.Name
		}
		call.arguments += chunk.Function.Arguments
		if call.id == "" || call.name == "" {
			continue
		}

		partial := streamingPartialWithToolCalls(thinking, text, provider, modelID, calls, *order)
		contentIndex := toolCallIndex(partial, call.id)
		if !call.started {
			_ = out.Push(protocol.AssistantMessageEvent{
				Type:         protocol.AssistantEventToolCallStart,
				ContentIndex: &contentIndex,
				Partial:      &partial,
			})
			call.started = true
		}
		if chunk.Function.Arguments != "" {
			_ = out.Push(protocol.AssistantMessageEvent{
				Type:         protocol.AssistantEventToolCallDelta,
				ContentIndex: &contentIndex,
				Delta:        chunk.Function.Arguments,
				Partial:      &partial,
			})
		}
	}
}

func streamingPartialWithToolCalls(thinking, text, provider, modelID string, calls map[int]*streamingToolCall, order []int) protocol.AssistantMessage {
	partial := streamingPartial(thinking, text, provider, modelID)
	for _, key := range order {
		call := calls[key]
		if call == nil || call.id == "" || call.name == "" {
			continue
		}
		partial.Content = append(partial.Content, protocol.NewToolCallContent(call.id, call.name, partialToolArguments(call.arguments)))
	}
	return partial
}

func partialToolArguments(arguments string) json.RawMessage {
	var object map[string]any
	if json.Unmarshal([]byte(arguments), &object) == nil && object != nil {
		return json.RawMessage(arguments)
	}
	return json.RawMessage(`{}`)
}

func partialAssistantMessage(chunks []*schema.Message, provider, modelID string) (protocol.AssistantMessage, error) {
	merged, err := schema.ConcatMessages(chunks)
	if err != nil {
		return protocol.AssistantMessage{}, err
	}
	if merged.Role == "" {
		merged.Role = schema.Assistant
	}
	converted, err := FromModelMessage(merged)
	if err != nil {
		return protocol.AssistantMessage{}, err
	}
	assistant, ok := protocol.AsAssistantMessage(converted)
	if !ok {
		return protocol.AssistantMessage{}, errors.New("model stream produced non-assistant message")
	}
	assistant.Provider = provider
	assistant.Model = modelID
	return assistant, nil
}

func pushTextChunkUpdates(out *protocol.AssistantMessageEventStream, message *schema.Message, text, thinking, provider, modelID string) (string, string) {
	if message.ReasoningContent != "" {
		if thinking == "" {
			partial := streamingPartial(thinking, text, provider, modelID)
			partial.Content = append(protocol.ContentList{protocol.NewThinkingContent("")}, partial.Content...)
			index := thinkingContentIndex(partial)
			_ = out.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventThinkingStart, ContentIndex: &index, Partial: &partial})
		}
		thinking += message.ReasoningContent
		partial := streamingPartial(thinking, text, provider, modelID)
		index := thinkingContentIndex(partial)
		_ = out.Push(protocol.AssistantMessageEvent{
			Type:         protocol.AssistantEventThinkingDelta,
			ContentIndex: &index,
			Delta:        message.ReasoningContent,
			Partial:      &partial,
		})
	}
	if message.Content != "" {
		if text == "" {
			partial := streamingPartial(thinking, text, provider, modelID)
			partial.Content = append(partial.Content, protocol.NewTextContent(""))
			index := textContentIndex(partial)
			_ = out.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventTextStart, ContentIndex: &index, Partial: &partial})
		}
		text += message.Content
		partial := streamingPartial(thinking, text, provider, modelID)
		index := textContentIndex(partial)
		_ = out.Push(protocol.AssistantMessageEvent{
			Type:         protocol.AssistantEventTextDelta,
			ContentIndex: &index,
			Delta:        message.Content,
			Partial:      &partial,
		})
	}
	return text, thinking
}

func pushFinalToolCalls(out *protocol.AssistantMessageEventStream, final protocol.AssistantMessage) {
	seen := map[string]bool{}
	for _, call := range protocol.ToolCallsFromAssistant(final) {
		if seen[call.ID] {
			continue
		}
		seen[call.ID] = true
		callCopy := protocol.CloneToolCallContent(call)
		contentIndex := toolCallIndex(final, call.ID)
		_ = out.Push(protocol.AssistantMessageEvent{
			Type:         protocol.AssistantEventToolCallEnd,
			ContentIndex: &contentIndex,
			ToolCall:     &callCopy,
			Partial:      &final,
		})
	}
}

func streamingPartial(thinking, text, provider, modelID string) protocol.AssistantMessage {
	content := protocol.ContentList{}
	if thinking != "" {
		content = append(content, protocol.NewThinkingContent(thinking))
	}
	if text != "" {
		content = append(content, protocol.NewTextContent(text))
	}
	return protocol.AssistantMessage{Content: content, Provider: provider, Model: modelID, StopReason: protocol.StopReasonStop}
}

func thinkingContentIndex(message protocol.AssistantMessage) int {
	for index, item := range message.Content {
		switch item.(type) {
		case protocol.ThinkingContent, *protocol.ThinkingContent:
			return index
		}
	}
	return 0
}

func textContentIndex(message protocol.AssistantMessage) int {
	for index, item := range message.Content {
		switch item.(type) {
		case protocol.TextContent, *protocol.TextContent:
			return index
		}
	}
	return 0
}

func pushAssistantError(out *protocol.AssistantMessageEventStream, err error, reason protocol.StopReason, provider, modelID string) {
	if reason != protocol.StopReasonAborted {
		reason = protocol.StopReasonError
	}
	messageText := cleanError(err.Error())
	message := protocol.AssistantMessage{
		Content:      protocol.ContentList{protocol.NewTextContent(messageText)},
		Provider:     provider,
		Model:        modelID,
		StopReason:   reason,
		ErrorMessage: messageText,
	}
	_ = out.Push(protocol.AssistantMessageEvent{Type: protocol.AssistantEventError, Reason: reason, Error: &message})
}

func cleanError(raw string) string {
	cleaned := providerPydanticTraceRE.ReplaceAllString(raw, "")
	cleaned = strings.ReplaceAll(cleaned, "\n\n\n", "\n\n")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return raw
	}
	return cleaned
}

func toolCallIndex(message protocol.AssistantMessage, id string) int {
	for index, item := range message.Content {
		switch value := item.(type) {
		case protocol.ToolCallContent:
			if value.ID == id {
				return index
			}
		case *protocol.ToolCallContent:
			if value != nil && value.ID == id {
				return index
			}
		}
	}
	return 0
}

func toolInfosFromDefinitions(definitions []protocol.ToolDefinition) ([]*schema.ToolInfo, error) {
	if len(definitions) == 0 {
		return nil, nil
	}
	infos := make([]*schema.ToolInfo, 0, len(definitions))
	for _, definition := range definitions {
		if definition.Name == "" {
			continue
		}
		info := &schema.ToolInfo{Name: definition.Name, Desc: definition.Description}
		if len(definition.Parameters) > 0 {
			params := &einojsonschema.Schema{}
			if err := json.Unmarshal(definition.Parameters, params); err != nil {
				return nil, err
			}
			info.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(params)
		}
		infos = append(infos, cloneToolInfo(info))
	}
	return infos, nil
}

func cloneToolInfos(infos []*schema.ToolInfo) []*schema.ToolInfo {
	out := make([]*schema.ToolInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, cloneToolInfo(info))
	}
	return out
}

func cloneToolInfo(info *schema.ToolInfo) *schema.ToolInfo {
	if info == nil {
		return nil
	}
	copied := *info
	return &copied
}

// FromModelMessage converts an Eino message to the Oops agent protocol.
func FromModelMessage(message *schema.Message) (protocol.AgentMessage, error) {
	if message == nil {
		return nil, ErrNilMessage
	}
	switch message.Role {
	case schema.System:
		return nil, ErrSystemBoundary
	case schema.User:
		content, err := fromModelUserContent(message)
		if err != nil {
			return nil, err
		}
		return protocol.UserMessage{Content: content}, nil
	case schema.Assistant:
		content, err := fromModelAssistantContent(message)
		if err != nil {
			return nil, err
		}
		return protocol.AssistantMessage{
			Content:      content,
			Usage:        fromModelUsage(message.ResponseMeta),
			StopReason:   fromModelStopReason(message.ResponseMeta, len(message.ToolCalls) > 0),
			ErrorMessage: fromModelErrorMessage(message.ResponseMeta),
		}, nil
	case schema.Tool:
		content, err := fromModelToolContent(message)
		if err != nil {
			return nil, err
		}
		return protocol.ToolResultMessage{
			ToolCallID: message.ToolCallID,
			ToolName:   message.ToolName,
			Content:    content,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported model role %q", message.Role)
	}
}

// ToModelMessage converts an Oops agent message to an Eino message.
func ToModelMessage(message protocol.AgentMessage) (*schema.Message, error) {
	if message == nil {
		return nil, errors.New("protocol message is nil")
	}
	if err := message.Validate(); err != nil {
		return nil, err
	}
	switch typed := message.(type) {
	case protocol.UserMessage:
		return toModelUserMessage(typed)
	case *protocol.UserMessage:
		return toModelUserMessage(*typed)
	case protocol.AssistantMessage:
		return toModelAssistantMessage(typed)
	case *protocol.AssistantMessage:
		return toModelAssistantMessage(*typed)
	case protocol.ToolResultMessage:
		return toModelToolMessage(typed)
	case *protocol.ToolResultMessage:
		return toModelToolMessage(*typed)
	default:
		return nil, fmt.Errorf("unsupported protocol message %T", message)
	}
}

func fromModelUserContent(message *schema.Message) (protocol.ContentList, error) {
	if len(message.UserInputMultiContent) > 0 {
		return fromModelInputParts(message.UserInputMultiContent)
	}
	return protocol.ContentList{protocol.NewTextContent(message.Content)}, nil
}

func fromModelAssistantContent(message *schema.Message) (protocol.ContentList, error) {
	content := make(protocol.ContentList, 0, len(message.AssistantGenMultiContent)+len(message.ToolCalls)+2)
	hasReasoningPart := false
	if len(message.AssistantGenMultiContent) > 0 {
		parts, err := fromModelOutputParts(message.AssistantGenMultiContent)
		if err != nil {
			return nil, err
		}
		hasReasoningPart = contentListHasThinking(parts)
		content = append(content, parts...)
	} else if message.Content != "" {
		content = append(content, protocol.NewTextContent(message.Content))
	}
	if message.ReasoningContent != "" && !hasReasoningPart {
		content = append(protocol.ContentList{protocol.NewThinkingContent(message.ReasoningContent)}, content...)
	}
	for _, call := range message.ToolCalls {
		arguments, err := rawToolArguments(call.Function.Arguments)
		if err != nil {
			return nil, err
		}
		content = append(content, protocol.NewToolCallContent(call.ID, call.Function.Name, arguments))
	}
	return content, nil
}

func fromModelToolContent(message *schema.Message) (protocol.ContentList, error) {
	if len(message.UserInputMultiContent) > 0 {
		return fromModelInputParts(message.UserInputMultiContent)
	}
	return protocol.ContentList{protocol.NewTextContent(message.Content)}, nil
}

func toModelUserMessage(message protocol.UserMessage) (*schema.Message, error) {
	if len(message.Content) == 1 {
		if text, ok := asText(message.Content[0]); ok {
			return schema.UserMessage(text.Text), nil
		}
	}
	parts := make([]schema.MessageInputPart, 0, len(message.Content))
	for _, content := range message.Content {
		switch typed := content.(type) {
		case protocol.TextContent:
			parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: typed.Text})
		case *protocol.TextContent:
			parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: typed.Text})
		case protocol.ImageContent:
			parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeImageURL, Image: toModelInputImage(typed)})
		case *protocol.ImageContent:
			parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeImageURL, Image: toModelInputImage(*typed)})
		default:
			return nil, fmt.Errorf("user message cannot contain %T", content)
		}
	}
	return &schema.Message{Role: schema.User, UserInputMultiContent: parts}, nil
}

func toModelAssistantMessage(message protocol.AssistantMessage) (*schema.Message, error) {
	out := &schema.Message{Role: schema.Assistant, ResponseMeta: toModelResponseMeta(message)}
	var textParts []string
	var outputParts []schema.MessageOutputPart
	hasOutputParts := false
	for _, content := range message.Content {
		switch typed := content.(type) {
		case protocol.TextContent:
			textParts = append(textParts, typed.Text)
			outputParts = append(outputParts, schema.MessageOutputPart{Type: schema.ChatMessagePartTypeText, Text: typed.Text})
		case *protocol.TextContent:
			textParts = append(textParts, typed.Text)
			outputParts = append(outputParts, schema.MessageOutputPart{Type: schema.ChatMessagePartTypeText, Text: typed.Text})
		case protocol.ThinkingContent:
			if out.ReasoningContent == "" {
				out.ReasoningContent = typed.Thinking
			}
		case *protocol.ThinkingContent:
			if out.ReasoningContent == "" {
				out.ReasoningContent = typed.Thinking
			}
		case protocol.ImageContent:
			outputParts = append(outputParts, schema.MessageOutputPart{Type: schema.ChatMessagePartTypeImageURL, Image: toModelOutputImage(typed)})
			hasOutputParts = true
		case *protocol.ImageContent:
			outputParts = append(outputParts, schema.MessageOutputPart{Type: schema.ChatMessagePartTypeImageURL, Image: toModelOutputImage(*typed)})
			hasOutputParts = true
		case protocol.ToolCallContent:
			out.ToolCalls = append(out.ToolCalls, toModelToolCall(typed))
		case *protocol.ToolCallContent:
			out.ToolCalls = append(out.ToolCalls, toModelToolCall(*typed))
		default:
			return nil, fmt.Errorf("assistant message cannot contain %T", content)
		}
	}
	out.Content = strings.Join(textParts, "")
	if hasOutputParts {
		out.AssistantGenMultiContent = outputParts
	}
	return out, nil
}

func toModelToolMessage(message protocol.ToolResultMessage) (*schema.Message, error) {
	out := schema.ToolMessage(protocol.TextFromContent(message.Content), message.ToolCallID, schema.WithToolName(message.ToolName))
	parts := make([]schema.MessageInputPart, 0, len(message.Content))
	for _, content := range message.Content {
		switch typed := content.(type) {
		case protocol.TextContent:
			parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: typed.Text})
		case *protocol.TextContent:
			parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: typed.Text})
		case protocol.ImageContent:
			parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeImageURL, Image: toModelInputImage(typed)})
		case *protocol.ImageContent:
			parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeImageURL, Image: toModelInputImage(*typed)})
		default:
			return nil, fmt.Errorf("tool result message cannot contain %T", content)
		}
	}
	if len(parts) > 1 || (len(parts) == 1 && parts[0].Type != schema.ChatMessagePartTypeText) {
		out.UserInputMultiContent = parts
	}
	return out, nil
}

func fromModelInputParts(parts []schema.MessageInputPart) (protocol.ContentList, error) {
	out := make(protocol.ContentList, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case schema.ChatMessagePartTypeText:
			out = append(out, protocol.NewTextContent(part.Text))
		case schema.ChatMessagePartTypeImageURL:
			if part.Image == nil {
				return nil, errors.New("image input part missing image")
			}
			out = append(out, fromModelInputImage(part.Image))
		default:
			return nil, fmt.Errorf("unsupported user input part %q", part.Type)
		}
	}
	return out, nil
}

func fromModelOutputParts(parts []schema.MessageOutputPart) (protocol.ContentList, error) {
	out := make(protocol.ContentList, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case schema.ChatMessagePartTypeText:
			out = append(out, protocol.NewTextContent(part.Text))
		case schema.ChatMessagePartTypeImageURL:
			if part.Image == nil {
				return nil, errors.New("image output part missing image")
			}
			out = append(out, fromModelOutputImage(part.Image))
		case schema.ChatMessagePartTypeReasoning:
			if part.Reasoning == nil {
				return nil, errors.New("reasoning output part missing reasoning")
			}
			out = append(out, protocol.ThinkingContent{
				Type:              protocol.ContentTypeThinking,
				Thinking:          part.Reasoning.Text,
				ThinkingSignature: part.Reasoning.Signature,
			})
		default:
			return nil, fmt.Errorf("unsupported assistant output part %q", part.Type)
		}
	}
	return out, nil
}

func fromModelInputImage(image *schema.MessageInputImage) protocol.ImageContent {
	content := protocol.ImageContent{Type: protocol.ContentTypeImage, MIMEType: image.MIMEType, Detail: string(image.Detail)}
	if image.URL != nil {
		content.URL = *image.URL
	}
	if image.Base64Data != nil {
		content.Data = *image.Base64Data
	}
	return content
}

func fromModelOutputImage(image *schema.MessageOutputImage) protocol.ImageContent {
	content := protocol.ImageContent{Type: protocol.ContentTypeImage, MIMEType: image.MIMEType}
	if image.URL != nil {
		content.URL = *image.URL
	}
	if image.Base64Data != nil {
		content.Data = *image.Base64Data
	}
	return content
}

func toModelInputImage(content protocol.ImageContent) *schema.MessageInputImage {
	return &schema.MessageInputImage{
		MessagePartCommon: schema.MessagePartCommon{
			URL:        ptrIfNotEmpty(content.URL),
			Base64Data: ptrIfNotEmpty(content.Data),
			MIMEType:   content.MIMEType,
		},
		Detail: schema.ImageURLDetail(content.Detail),
	}
}

func toModelOutputImage(content protocol.ImageContent) *schema.MessageOutputImage {
	return &schema.MessageOutputImage{MessagePartCommon: schema.MessagePartCommon{
		URL:        ptrIfNotEmpty(content.URL),
		Base64Data: ptrIfNotEmpty(content.Data),
		MIMEType:   content.MIMEType,
	}}
}

func toModelToolCall(content protocol.ToolCallContent) schema.ToolCall {
	return schema.ToolCall{ID: content.ID, Type: "function", Function: schema.FunctionCall{Name: content.Name, Arguments: string(content.Arguments)}}
}

func fromModelUsage(meta *schema.ResponseMeta) protocol.Usage {
	if meta == nil || meta.Usage == nil {
		return protocol.Usage{}
	}
	usage := meta.Usage
	reasoning := usage.CompletionTokensDetails.ReasoningTokens
	out := protocol.Usage{
		Input:       usage.PromptTokens,
		Output:      usage.CompletionTokens,
		CacheRead:   usage.PromptTokenDetails.CachedTokens,
		TotalTokens: usage.TotalTokens,
	}
	if reasoning != 0 {
		out.Reasoning = &reasoning
	}
	return out
}

func toModelResponseMeta(message protocol.AssistantMessage) *schema.ResponseMeta {
	return &schema.ResponseMeta{
		FinishReason: toModelFinishReason(message.StopReason),
		Usage: &schema.TokenUsage{
			PromptTokens:     message.Usage.Input,
			CompletionTokens: message.Usage.Output,
			TotalTokens:      message.Usage.TotalTokens,
			PromptTokenDetails: schema.PromptTokenDetails{
				CachedTokens: message.Usage.CacheRead,
			},
			CompletionTokensDetails: schema.CompletionTokensDetails{ReasoningTokens: intValue(message.Usage.Reasoning)},
		},
	}
}

func fromModelStopReason(meta *schema.ResponseMeta, hasToolCalls bool) protocol.StopReason {
	if meta == nil {
		if hasToolCalls {
			return protocol.StopReasonToolUse
		}
		return protocol.StopReasonStop
	}
	switch meta.FinishReason {
	case "", "stop":
		if hasToolCalls {
			return protocol.StopReasonToolUse
		}
		return protocol.StopReasonStop
	case "length":
		return protocol.StopReasonLength
	case "tool_calls", "tool_use", "toolUse":
		return protocol.StopReasonToolUse
	case "error":
		return protocol.StopReasonError
	case "aborted":
		return protocol.StopReasonAborted
	default:
		return protocol.StopReasonError
	}
}

func toModelFinishReason(reason protocol.StopReason) string {
	switch reason {
	case protocol.StopReasonStop:
		return "stop"
	case protocol.StopReasonLength:
		return "length"
	case protocol.StopReasonToolUse:
		return "tool_calls"
	case protocol.StopReasonError:
		return "error"
	case protocol.StopReasonAborted:
		return "aborted"
	default:
		return ""
	}
}

func fromModelErrorMessage(meta *schema.ResponseMeta) string {
	if meta == nil {
		return ""
	}
	switch meta.FinishReason {
	case "", "stop", "length", "tool_calls", "tool_use", "toolUse", "error", "aborted":
		return ""
	default:
		return "finish_reason: " + meta.FinishReason
	}
}

func rawToolArguments(arguments string) (json.RawMessage, error) {
	// Providers may omit arguments for no-argument tool calls.
	if strings.TrimSpace(arguments) == "" {
		arguments = "{}"
	}
	raw := json.RawMessage(arguments)
	if !json.Valid(raw) {
		return nil, fmt.Errorf("tool call arguments must be valid json: %q", arguments)
	}
	return raw, nil
}

func asText(content protocol.Content) (protocol.TextContent, bool) {
	switch typed := content.(type) {
	case protocol.TextContent:
		return typed, true
	case *protocol.TextContent:
		return *typed, true
	default:
		return protocol.TextContent{}, false
	}
}

func contentListHasThinking(content protocol.ContentList) bool {
	for _, item := range content {
		switch item.(type) {
		case protocol.ThinkingContent, *protocol.ThinkingContent:
			return true
		}
	}
	return false
}

func ptrIfNotEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

package einoadapter

import (
	"strings"

	"oops/internal/llm/ai/protocol"

	"github.com/cloudwego/eino/schema"
)

func FromEinoContext(messages []*schema.Message) (protocol.Context, error) {
	var systemPrompts []string
	agentMessages := make(protocol.MessageList, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			return protocol.Context{}, ErrNilMessage
		}
		if msg.Role == schema.System {
			if msg.Content != "" {
				systemPrompts = append(systemPrompts, msg.Content)
			}
			continue
		}
		converted, err := FromEinoMessage(msg)
		if err != nil {
			return protocol.Context{}, err
		}
		agentMessages = append(agentMessages, converted)
	}
	return protocol.Context{
		SystemPrompt: strings.Join(systemPrompts, "\n\n"),
		Messages:     agentMessages,
	}, nil
}

func ToEinoContext(ctx protocol.Context) ([]*schema.Message, error) {
	messages := make([]*schema.Message, 0, len(ctx.Messages)+1)
	if ctx.SystemPrompt != "" {
		messages = append(messages, schema.SystemMessage(ctx.SystemPrompt))
	}
	converted, err := ToEinoMessages(ctx.Messages)
	if err != nil {
		return nil, err
	}
	messages = append(messages, converted...)
	return messages, nil
}

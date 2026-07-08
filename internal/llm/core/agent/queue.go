package agent

import (
	"fmt"

	"oops/internal/llm/ai/protocol"
)

type QueueMode string

const (
	QueueModeOneAtATime QueueMode = "one-at-a-time"
	QueueModeAll        QueueMode = "all"
)

type messageQueue struct {
	mode     QueueMode
	messages protocol.MessageList
}

func newMessageQueue(mode QueueMode) messageQueue {
	if mode == "" {
		mode = QueueModeOneAtATime
	}
	return messageQueue{mode: mode}
}

func validateQueueMode(mode QueueMode) error {
	if mode == QueueModeOneAtATime || mode == QueueModeAll {
		return nil
	}
	return fmt.Errorf("unknown queue mode %q", mode)
}

func (q *messageQueue) enqueue(messages protocol.MessageList) error {
	cloned, err := cloneValidatedMessages(messages)
	if err != nil {
		return err
	}
	q.messages = append(q.messages, cloned...)
	return nil
}

func (q *messageQueue) drain() protocol.MessageList {
	if len(q.messages) == 0 {
		return nil
	}
	if q.mode == QueueModeAll {
		drained := cloneMessages(q.messages)
		q.messages = nil
		return drained
	}
	first := cloneMessages(q.messages[:1])
	q.messages = cloneMessages(q.messages[1:])
	return first
}

func (q *messageQueue) clear() {
	q.messages = nil
}

func (q *messageQueue) len() int {
	return len(q.messages)
}

func (q *messageQueue) hasItems() bool {
	return len(q.messages) > 0
}

func (q *messageQueue) setMode(mode QueueMode) error {
	if mode == "" {
		mode = QueueModeOneAtATime
	}
	if err := validateQueueMode(mode); err != nil {
		return err
	}
	q.mode = mode
	return nil
}

func cloneValidatedMessages(messages protocol.MessageList) (protocol.MessageList, error) {
	for _, message := range messages {
		if message == nil {
			return nil, fmt.Errorf("message list contains nil message")
		}
		if err := message.Validate(); err != nil {
			return nil, err
		}
	}
	return cloneMessages(messages), nil
}

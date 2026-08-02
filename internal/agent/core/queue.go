package core

import (
	"fmt"

	protocol "oops/internal/agent/ai"
)

type messageQueue struct {
	mode     QueueMode
	messages protocol.MessageList
}

func newMessageQueue(mode QueueMode) messageQueue {
	return messageQueue{mode: normalizeQueueMode(mode)}
}

func (q *messageQueue) enqueue(messages protocol.MessageList) error {
	validated, err := cloneValidatedMessages(messages)
	if err != nil {
		return err
	}
	if len(validated) == 0 {
		return fmt.Errorf("queued messages are required")
	}
	q.messages = append(q.messages, validated...)
	return nil
}

func (q *messageQueue) drain() protocol.MessageList {
	if len(q.messages) == 0 {
		return nil
	}
	count := len(q.messages)
	if normalizeQueueMode(q.mode) == QueueModeOneAtATime {
		count = 1
	}
	drained := cloneMessages(q.messages[:count])
	q.messages = append(protocol.MessageList(nil), q.messages[count:]...)
	return drained
}

func (q *messageQueue) clear() {
	q.messages = nil
}

func (q *messageQueue) takeAll() protocol.MessageList {
	messages := cloneMessages(q.messages)
	q.clear()
	return messages
}

func (q *messageQueue) hasItems() bool {
	return len(q.messages) > 0
}

func (q *messageQueue) setMode(mode QueueMode) error {
	if err := validateQueueMode(mode); err != nil {
		return err
	}
	q.mode = normalizeQueueMode(mode)
	return nil
}

func validateQueueMode(mode QueueMode) error {
	switch mode {
	case "", QueueModeOneAtATime, QueueModeAll:
		return nil
	default:
		return fmt.Errorf("unknown queue mode %q", mode)
	}
}

func normalizeQueueMode(mode QueueMode) QueueMode {
	if mode == QueueModeAll {
		return QueueModeAll
	}
	return QueueModeOneAtATime
}

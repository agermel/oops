package protocol

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrStreamClosed             = errors.New("assistant message event stream is closed")
	ErrStreamEndedWithoutResult = errors.New("assistant message event stream ended without result")
)

type AssistantMessageEventStream struct {
	events chan AssistantMessageEvent
	done   chan struct{}
	cond   *sync.Cond

	mu       sync.Mutex
	closed   bool
	doneDone bool
	final    *AssistantMessage
	finalErr error
	queue    []AssistantMessageEvent
}

func NewAssistantMessageEventStream(buffer int) *AssistantMessageEventStream {
	if buffer < 0 {
		buffer = 0
	}
	stream := &AssistantMessageEventStream{
		events: make(chan AssistantMessageEvent, buffer),
		done:   make(chan struct{}),
	}
	stream.cond = sync.NewCond(&stream.mu)
	go stream.pump()
	return stream
}

func (s *AssistantMessageEventStream) Events() <-chan AssistantMessageEvent {
	return s.events
}

func (s *AssistantMessageEventStream) Push(event AssistantMessageEvent) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrStreamClosed
	}
	s.mu.Unlock()

	if err := event.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStreamClosed
	}

	if final, ok := event.FinalMessage(); ok {
		s.final = CloneAssistantMessagePtr(final)
		s.closed = true
		s.closeDoneLocked()
	}

	s.queue = append(s.queue, event)
	s.cond.Signal()
	return nil
}

func (s *AssistantMessageEventStream) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.finalErr = ErrStreamEndedWithoutResult
	s.closeDoneLocked()
	s.cond.Signal()
	s.mu.Unlock()
}

func (s *AssistantMessageEventStream) Result(ctx context.Context) (*AssistantMessage, error) {
	select {
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.final != nil {
			return s.final, nil
		}
		if s.finalErr != nil {
			return nil, s.finalErr
		}
		return nil, ErrStreamEndedWithoutResult
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *AssistantMessageEventStream) pump() {
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && !s.closed {
			s.cond.Wait()
		}
		if len(s.queue) == 0 && s.closed {
			s.mu.Unlock()
			close(s.events)
			return
		}
		event := s.queue[0]
		s.queue[0] = AssistantMessageEvent{}
		s.queue = s.queue[1:]
		closeAfterSend := s.closed && len(s.queue) == 0
		s.mu.Unlock()

		s.events <- event

		if closeAfterSend {
			close(s.events)
			return
		}
	}
}

func (s *AssistantMessageEventStream) closeDoneLocked() {
	if s.doneDone {
		return
	}
	s.doneDone = true
	close(s.done)
}

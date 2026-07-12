package mcp

import "slices"

const notificationQueueCapacity = 32

type toolChange struct {
	revision int64
	tools    []ConnectionTool
}

func (m *Manager) startNotificationDispatcher() {
	if m.onChange == nil {
		return
	}
	m.notificationCh = make(chan *toolChange, notificationQueueCapacity)
	m.notificationDone = make(chan struct{})
	go func() {
		defer close(m.notificationDone)
		for change := range m.notificationCh {
			m.onChange(slices.Clone(change.tools))
		}
	}()
}

// enqueueToolChange appends one committed immutable snapshot. Callers hold
// mutationMu, which orders revisions with Close and applies bounded
// backpressure when callbacks fall behind.
func (m *Manager) enqueueToolChange(change *toolChange) {
	if change == nil {
		return
	}
	m.notificationMu.Lock()
	defer m.notificationMu.Unlock()
	if m.notificationStop || m.notificationCh == nil {
		return
	}
	m.notificationCh <- &toolChange{revision: change.revision, tools: slices.Clone(change.tools)}
}

// stopNotificationDispatcher prevents future notification work and returns the
// running dispatcher's completion signal. Callers must wait only after
// releasing mutationMu so callbacks may continue to call Manager query methods.
func (m *Manager) stopNotificationDispatcher() <-chan struct{} {
	if m.notificationCh == nil {
		return nil
	}
	m.notificationMu.Lock()
	defer m.notificationMu.Unlock()
	m.notificationStop = true
	close(m.notificationCh)
	return m.notificationDone
}

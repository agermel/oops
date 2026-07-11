package mcp

import "slices"

type toolChange struct {
	revision int64
	tools    []ConnectionTool
}

func (m *Manager) startNotificationDispatcher() {
	if m.onChange == nil {
		return
	}
	m.notificationCh = make(chan struct{}, 1)
	m.notificationDone = make(chan struct{})
	go func() {
		defer close(m.notificationDone)
		for range m.notificationCh {
			for {
				m.notificationMu.Lock()
				if len(m.pendingChanges) == 0 {
					m.notificationMu.Unlock()
					break
				}
				change := m.pendingChanges[0]
				m.pendingChanges[0] = toolChange{}
				m.pendingChanges = m.pendingChanges[1:]
				m.notificationMu.Unlock()

				m.mu.Lock()
				closed := m.closed
				m.mu.Unlock()
				if closed {
					continue
				}
				m.onChange(slices.Clone(change.tools))
			}
		}
	}()
}

// enqueueToolChange retains every committed immutable snapshot in revision
// order. Callers hold mutationMu, which orders revisions with Close.
func (m *Manager) enqueueToolChange(change *toolChange) {
	if change == nil {
		return
	}
	m.notificationMu.Lock()
	defer m.notificationMu.Unlock()
	if m.notificationStop || m.notificationCh == nil {
		return
	}
	copied := toolChange{revision: change.revision, tools: slices.Clone(change.tools)}
	m.pendingChanges = append(m.pendingChanges, copied)
	select {
	case m.notificationCh <- struct{}{}:
	default:
	}
}

func (m *Manager) closeNotificationDispatcher() {
	if m.notificationCh == nil {
		return
	}
	m.notificationMu.Lock()
	m.notificationStop = true
	m.pendingChanges = nil
	close(m.notificationCh)
	m.notificationMu.Unlock()
	<-m.notificationDone
	m.notificationCh = nil
}

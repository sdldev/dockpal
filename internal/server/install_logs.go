package server

import (
	"fmt"
	"sync"
)

// LogSession represents a single active installation session's logs.
type LogSession struct {
	mu        sync.RWMutex
	Logs      []string
	Listeners []chan string
	Completed bool
}

// InstallLogsManager manages active SSH installation logs in memory.
type InstallLogsManager struct {
	mu       sync.RWMutex
	sessions map[string]*LogSession
}

// NewInstallLogsManager creates a new logs manager.
func NewInstallLogsManager() *InstallLogsManager {
	return &InstallLogsManager{
		sessions: make(map[string]*LogSession),
	}
}

// GetOrCreateSession retrieves or creates a LogSession for an instance.
func (m *InstallLogsManager) GetOrCreateSession(instanceID string) *LogSession {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, exists := m.sessions[instanceID]
	if !exists {
		session = &LogSession{
			Logs:      make([]string, 0),
			Listeners: make([]chan string, 0),
		}
		m.sessions[instanceID] = session
	}
	return session
}

// WriteLog appends a new log line and broadcasts it to all listeners.
func (m *InstallLogsManager) WriteLog(instanceID string, message string) {
	session := m.GetOrCreateSession(instanceID)

	session.mu.Lock()
	session.Logs = append(session.Logs, message)
	listeners := make([]chan string, len(session.Listeners))
	copy(listeners, session.Listeners)
	session.mu.Unlock()

	// Broadcast to all active listeners
	for _, ch := range listeners {
		select {
		case ch <- message:
		default:
			// listener channel full or blocked, skip
		}
	}
}

// WriteLogf formatting helper for WriteLog.
func (m *InstallLogsManager) WriteLogf(instanceID string, format string, args ...interface{}) {
	m.WriteLog(instanceID, fmt.Sprintf(format, args...))
}

// RegisterListener registers a new listener channel for real-time logs.
// Returns the channel, current log history, and a deregister function.
func (m *InstallLogsManager) RegisterListener(instanceID string) (chan string, []string, func()) {
	session := m.GetOrCreateSession(instanceID)

	ch := make(chan string, 100)

	session.mu.Lock()
	session.Listeners = append(session.Listeners, ch)
	// Copy current logs for safe access outside mutex
	history := make([]string, len(session.Logs))
	copy(history, session.Logs)
	session.mu.Unlock()

	deregister := func() {
		session.mu.Lock()
		defer session.mu.Unlock()
		for i, listener := range session.Listeners {
			if listener == ch {
				// Remove listener
				session.Listeners = append(session.Listeners[:i], session.Listeners[i+1:]...)
				close(ch)
				break
			}
		}
	}

	return ch, history, deregister
}

// CompleteSession marks the session as completed.
func (m *InstallLogsManager) CompleteSession(instanceID string) {
	session := m.GetOrCreateSession(instanceID)
	session.mu.Lock()
	session.Completed = true
	session.mu.Unlock()
}

// RemoveSession deletes logs from memory.
func (m *InstallLogsManager) RemoveSession(instanceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, instanceID)
}

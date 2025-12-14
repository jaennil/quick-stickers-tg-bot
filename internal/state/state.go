package state

import (
	"context"
	"sync"
	"time"
)

type AwaitingMode int

const (
	ModeNone AwaitingMode = iota
	ModeEdit
	ModeSearch
	ModeAddPack
)

type UserState struct {
	LastStickerID  string
	AwaitingMode   AwaitingMode
	ActiveIndexing context.CancelFunc
	UpdatedAt      time.Time
}

type OCRResult struct {
	Results   map[string]string
	CreatedAt time.Time
}

type Manager struct {
	users      map[int64]*UserState
	usersMu    sync.RWMutex
	pendingOCR map[string]*OCRResult
	ocrMu      sync.RWMutex
	ttl        time.Duration
}

func NewManager(ttl time.Duration) *Manager {
	m := &Manager{
		users:      make(map[int64]*UserState),
		pendingOCR: make(map[string]*OCRResult),
		ttl:        ttl,
	}
	go m.cleanupLoop()
	return m
}

func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		m.cleanup()
	}
}

func (m *Manager) cleanup() {
	now := time.Now()

	m.ocrMu.Lock()
	for id, result := range m.pendingOCR {
		if now.Sub(result.CreatedAt) > m.ttl {
			delete(m.pendingOCR, id)
		}
	}
	m.ocrMu.Unlock()

	m.usersMu.Lock()
	for id, state := range m.users {
		if state.ActiveIndexing == nil && now.Sub(state.UpdatedAt) > m.ttl {
			delete(m.users, id)
		}
	}
	m.usersMu.Unlock()
}

func (m *Manager) getOrCreateUser(userID int64) *UserState {
	m.usersMu.Lock()
	defer m.usersMu.Unlock()
	if state, ok := m.users[userID]; ok {
		state.UpdatedAt = time.Now()
		return state
	}
	state := &UserState{UpdatedAt: time.Now()}
	m.users[userID] = state
	return state
}

func (m *Manager) SetLastSticker(userID int64, stickerID string) {
	state := m.getOrCreateUser(userID)
	state.LastStickerID = stickerID
}

func (m *Manager) GetLastSticker(userID int64) string {
	m.usersMu.RLock()
	defer m.usersMu.RUnlock()
	if state, ok := m.users[userID]; ok {
		return state.LastStickerID
	}
	return ""
}

func (m *Manager) SetAwaitingMode(userID int64, mode AwaitingMode) {
	state := m.getOrCreateUser(userID)
	state.AwaitingMode = mode
}

func (m *Manager) GetAwaitingMode(userID int64) AwaitingMode {
	m.usersMu.RLock()
	defer m.usersMu.RUnlock()
	if state, ok := m.users[userID]; ok {
		return state.AwaitingMode
	}
	return ModeNone
}

func (m *Manager) ClearAwaitingMode(userID int64) {
	m.usersMu.Lock()
	defer m.usersMu.Unlock()
	if state, ok := m.users[userID]; ok {
		state.AwaitingMode = ModeNone
		state.UpdatedAt = time.Now()
	}
}

func (m *Manager) SetActiveIndexing(userID int64, cancel context.CancelFunc) {
	state := m.getOrCreateUser(userID)
	state.ActiveIndexing = cancel
}

func (m *Manager) GetActiveIndexing(userID int64) (context.CancelFunc, bool) {
	m.usersMu.RLock()
	defer m.usersMu.RUnlock()
	if state, ok := m.users[userID]; ok && state.ActiveIndexing != nil {
		return state.ActiveIndexing, true
	}
	return nil, false
}

func (m *Manager) ClearActiveIndexing(userID int64) {
	m.usersMu.Lock()
	defer m.usersMu.Unlock()
	if state, ok := m.users[userID]; ok {
		state.ActiveIndexing = nil
		state.UpdatedAt = time.Now()
	}
}

func (m *Manager) HasActiveIndexing(userID int64) bool {
	_, ok := m.GetActiveIndexing(userID)
	return ok
}

func (m *Manager) SetPendingOCR(stickerID string, results map[string]string) {
	m.ocrMu.Lock()
	defer m.ocrMu.Unlock()
	m.pendingOCR[stickerID] = &OCRResult{
		Results:   results,
		CreatedAt: time.Now(),
	}
}

func (m *Manager) GetPendingOCR(stickerID string) (map[string]string, bool) {
	m.ocrMu.RLock()
	defer m.ocrMu.RUnlock()
	if result, ok := m.pendingOCR[stickerID]; ok {
		return result.Results, true
	}
	return nil, false
}

func (m *Manager) DeletePendingOCR(stickerID string) {
	m.ocrMu.Lock()
	defer m.ocrMu.Unlock()
	delete(m.pendingOCR, stickerID)
}

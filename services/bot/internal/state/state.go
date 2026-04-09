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

type PackSize struct {
	Total     int
	CreatedAt time.Time
}

type Manager struct {
	users     map[int64]*UserState
	usersMu   sync.RWMutex
	packSizes map[string]*PackSize
	packMu    sync.RWMutex
	ttl       time.Duration
}

func NewManager(ttl time.Duration) *Manager {
	m := &Manager{
		users:     make(map[int64]*UserState),
		packSizes: make(map[string]*PackSize),
		ttl:       ttl,
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

	m.packMu.Lock()
	for setName, pack := range m.packSizes {
		if now.Sub(pack.CreatedAt) > m.ttl {
			delete(m.packSizes, setName)
		}
	}
	m.packMu.Unlock()

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

func (m *Manager) SetPackSize(setName string, total int) {
	if setName == "" || total <= 0 {
		return
	}

	m.packMu.Lock()
	defer m.packMu.Unlock()
	m.packSizes[setName] = &PackSize{
		Total:     total,
		CreatedAt: time.Now(),
	}
}

func (m *Manager) GetPackSize(setName string) (int, bool) {
	m.packMu.RLock()
	defer m.packMu.RUnlock()
	pack, ok := m.packSizes[setName]
	if !ok {
		return 0, false
	}
	return pack.Total, true
}

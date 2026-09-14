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
	// LastStickerID is the most recently received media, overwritten by every
	// incoming one. It backs the /edit command.
	LastStickerID string
	// PendingEditID is the media an edit button was pressed on. It is kept
	// apart from LastStickerID precisely because incoming media must not
	// redirect an edit the user already started.
	PendingEditID  string
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

// withUser mutates a user's state while holding the write lock. Returning the
// struct to the caller instead would let the mutation happen after unlocking.
func (m *Manager) withUser(userID int64, mutate func(*UserState)) {
	m.usersMu.Lock()
	defer m.usersMu.Unlock()
	state, ok := m.users[userID]
	if !ok {
		state = &UserState{}
		m.users[userID] = state
	}
	mutate(state)
	state.UpdatedAt = time.Now()
}

func (m *Manager) SetLastSticker(userID int64, stickerID string) {
	m.withUser(userID, func(s *UserState) { s.LastStickerID = stickerID })
}

// SetAwaitingEdit records both that the user is typing a correction and which
// media it is for, so the two can never drift apart.
func (m *Manager) SetAwaitingEdit(userID int64, stickerID string) {
	m.withUser(userID, func(s *UserState) {
		s.PendingEditID = stickerID
		s.AwaitingMode = ModeEdit
	})
}

// TakeAwaitingEdit returns the media awaiting a correction and clears it in the
// same critical section, so a reply cannot be applied twice or to the wrong one.
func (m *Manager) TakeAwaitingEdit(userID int64) (string, bool) {
	m.usersMu.Lock()
	defer m.usersMu.Unlock()
	state, ok := m.users[userID]
	if !ok || state.AwaitingMode != ModeEdit || state.PendingEditID == "" {
		return "", false
	}
	stickerID := state.PendingEditID
	state.PendingEditID = ""
	state.AwaitingMode = ModeNone
	state.UpdatedAt = time.Now()
	return stickerID, true
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
	m.withUser(userID, func(s *UserState) { s.AwaitingMode = mode })
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
		state.PendingEditID = ""
		state.UpdatedAt = time.Now()
	}
}

func (m *Manager) SetActiveIndexing(userID int64, cancel context.CancelFunc) {
	m.withUser(userID, func(s *UserState) { s.ActiveIndexing = cancel })
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

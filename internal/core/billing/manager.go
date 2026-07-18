package billing

import (
	"errors"
	"sync"
)

var ErrUserNotFound = errors.New("billing user not found")

// Manager is a concurrency-safe username-to-user registry used by submit-time
// orchestration. Persistent repositories remain the authority for durable state.
type Manager struct {
	mu    sync.RWMutex
	users map[string]*User
}

func NewManager() *Manager {
	return &Manager{users: make(map[string]*User)}
}

func (manager *Manager) AddUser(username string, user *User) error {
	if username == "" || user == nil {
		return ErrUserNotFound
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.users[username] = user
	return nil
}

func (manager *Manager) GetUser(username string) (*User, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	user, ok := manager.users[username]
	if !ok || user == nil {
		return nil, ErrUserNotFound
	}
	return user, nil
}

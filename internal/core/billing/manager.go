package billing

import (
	"errors"
	"strconv"
	"sync"
)

var (
	ErrUserNotFound   = errors.New("billing user not found")
	ErrUserIDConflict = errors.New("billing user ID already registered")
)

// Manager is a concurrency-safe username/external-user-ID registry used by
// submit-time and late-response orchestration. External IDs preserve legacy
// opaque Jasmin UIDs while User keeps its internal numeric identity.
type Manager struct {
	mu        sync.RWMutex
	users     map[string]*User
	usersByID map[string]*User
	userIDs   map[string]string
	idOwners  map[string]string
}

func NewManager() *Manager {
	return &Manager{
		users:     make(map[string]*User),
		usersByID: make(map[string]*User),
		userIDs:   make(map[string]string),
		idOwners:  make(map[string]string),
	}
}

func (manager *Manager) AddUser(username string, user *User) error {
	if user == nil {
		return ErrUserNotFound
	}
	return manager.AddUserWithID(username, strconv.FormatInt(user.UID(), 10), user)
}

func (manager *Manager) AddUserWithID(username, userID string, user *User) error {
	if username == "" || userID == "" || user == nil {
		return ErrUserNotFound
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if owner, exists := manager.idOwners[userID]; exists && owner != username {
		return ErrUserIDConflict
	}
	if previousID, exists := manager.userIDs[username]; exists && previousID != userID {
		delete(manager.usersByID, previousID)
		delete(manager.idOwners, previousID)
	}
	manager.users[username] = user
	manager.usersByID[userID] = user
	manager.userIDs[username] = userID
	manager.idOwners[userID] = username
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

func (manager *Manager) GetUserByID(userID string) (*User, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	user, ok := manager.usersByID[userID]
	if !ok || user == nil {
		return nil, ErrUserNotFound
	}
	return user, nil
}

package relay

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type RelayUser struct {
	ID           string    `json:"id"`
	Key          string    `json:"key"`
	PasswordHash string    `json:"passwordHash"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"createdAt"`
	Remark       string    `json:"remark,omitempty"`
}

type UserManager struct {
	sync.RWMutex
	users       []*RelayUser
	persistPath string
}

func NewUserManager() *UserManager {
	return &UserManager{
		users: make([]*RelayUser, 0),
	}
}

func (m *UserManager) Init(dataDir string) {
	m.Lock()
	m.persistPath = filepath.Join(dataDir, "relay_users.json")
	m.Unlock()

	m.LoadFromDisk()
}

func (m *UserManager) AddUser(key, password, remark string) (*RelayUser, error) {
	m.Lock()
	defer m.Unlock()

	for _, u := range m.users {
		if u.Key == key {
			return nil, fmt.Errorf("user key %q already exists", key)
		}
	}

	user := &RelayUser{
		ID:           generateID(),
		Key:          key,
		PasswordHash: hashPassword(password),
		Enabled:      true,
		CreatedAt:    time.Now(),
		Remark:       remark,
	}
	m.users = append(m.users, user)
	m.saveToDiskLocked()
	return user, nil
}

func (m *UserManager) RemoveUser(id string) error {
	m.Lock()
	defer m.Unlock()

	for i, u := range m.users {
		if u.ID == id {
			m.users = append(m.users[:i], m.users[i+1:]...)
			m.saveToDiskLocked()
			return nil
		}
	}
	return fmt.Errorf("user %q not found", id)
}

func (m *UserManager) UpdateUserEnabled(id string, enabled bool) {
	m.Lock()
	defer m.Unlock()

	for _, u := range m.users {
		if u.ID == id {
			u.Enabled = enabled
			m.saveToDiskLocked()
			return
		}
	}
}

func (m *UserManager) GetUsers() []*RelayUser {
	m.RLock()
	defer m.RUnlock()

	result := make([]*RelayUser, len(m.users))
	for i, u := range m.users {
		copied := *u
		copied.PasswordHash = "***"
		result[i] = &copied
	}
	return result
}

func (m *UserManager) GetUserByID(id string) *RelayUser {
	m.RLock()
	defer m.RUnlock()

	for _, u := range m.users {
		if u.ID == id {
			return u
		}
	}
	return nil
}

func (m *UserManager) ValidateCredentials(key, password string) (*RelayUser, error) {
	m.RLock()
	defer m.RUnlock()

	for _, u := range m.users {
		if u.Key == key {
			if !checkPassword(u.PasswordHash, password) {
				return nil, fmt.Errorf("invalid password")
			}
			if !u.Enabled {
				return nil, fmt.Errorf("user %q is disabled", key)
			}
			return u, nil
		}
	}
	return nil, fmt.Errorf("user %q not found", key)
}

func (m *UserManager) saveToDiskLocked() {
	if m.persistPath == "" {
		return
	}
	data, err := json.MarshalIndent(m.users, "", "  ")
	if err != nil {
		fmt.Printf("[UserManager] Failed to marshal users: %v\n", err)
		return
	}
	if err := os.WriteFile(m.persistPath, data, 0644); err != nil {
		fmt.Printf("[UserManager] Failed to write users: %v\n", err)
	}
}

func (m *UserManager) SaveToDisk() {
	m.RLock()
	defer m.RUnlock()
	m.saveToDiskLocked()
}

func (m *UserManager) LoadFromDisk() {
	m.Lock()
	defer m.Unlock()

	if m.persistPath == "" {
		m.users = make([]*RelayUser, 0)
		return
	}

	if _, err := os.Stat(m.persistPath); os.IsNotExist(err) {
		m.users = make([]*RelayUser, 0)
		return
	}

	raw, err := os.ReadFile(m.persistPath)
	if err != nil {
		m.users = make([]*RelayUser, 0)
		return
	}

	var loaded []*RelayUser
	if err := json.Unmarshal(raw, &loaded); err != nil {
		m.users = make([]*RelayUser, 0)
		return
	}
	m.users = loaded
}

func (m *UserManager) UpdatePath(newDir string) {
	m.SaveToDisk()

	m.Lock()
	m.persistPath = filepath.Join(newDir, "relay_users.json")
	m.Unlock()

	m.LoadFromDisk()
}

func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("failed to generate random ID: %v", err))
	}
	return hex.EncodeToString(b)
}

func hashPassword(password string) string {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		panic(fmt.Sprintf("failed to hash password: %v", err))
	}
	return string(hash)
}

func checkPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

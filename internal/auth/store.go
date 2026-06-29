package auth

import (
	"fmt"
	"os"
	"sync"
	"time"

	"oops/internal/logutil"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// User 表示一个可登录的用户。
type User struct {
	Username string `yaml:"-"`      // 登录用户名（YAML map 的 key），不在 YAML 中序列化
	Name     string `yaml:"name"`    // 显示名称
	Password string `yaml:"password"` // bcrypt hash
}

// Store 管理用户凭证，从 YAML 文件加载，支持热加载。
type Store struct {
	mu      sync.RWMutex
	path    string
	modTime time.Time
	Users   map[string]*User `yaml:"users"` // username → User
}

// NewStore 从 YAML 文件加载用户。文件不存在时返回空 store。
func NewStore(path string) (*Store, error) {
	s := &Store{path: path, Users: make(map[string]*User)}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return s, nil
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// IsEmpty 返回 store 中是否没有配置任何用户。带读锁保护，可安全跨 goroutine 使用。
func (s *Store) IsEmpty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.Users) == 0
}

// Validate 验证用户名和密码。返回用户信息或错误。
func (s *Store) Validate(username, password string) (*User, error) {
	if err := s.reloadIfChanged(); err != nil {
		return nil, fmt.Errorf("reload users: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	u, ok := s.Users[username]
	if !ok {
		return nil, fmt.Errorf("invalid username or password")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)); err != nil {
		return nil, fmt.Errorf("invalid username or password")
	}
	u.Username = username // 确保 Username 字段被填充（防御性编程）
	return u, nil
}

// Find 按用户名查找用户（不验证密码）。
func (s *Store) Find(username string) *User {
	if err := s.reloadIfChanged(); err != nil {
		logutil.Errorf("auth: reload users failed: %v", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Users[username]
}

// reloadIfChanged 在文件 mtime 变化时重新加载。
func (s *Store) reloadIfChanged() error {
	info, err := os.Stat(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// 读锁保护 s.modTime，避免与 load() 中的写锁并发造成 data race。
	s.mu.RLock()
	changed := info.ModTime().After(s.modTime)
	s.mu.RUnlock()

	if !changed {
		return nil
	}
	return s.load()
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}

	var raw struct {
		Users map[string]*User `yaml:"users"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse users.yml: %w", err)
	}

	info, _ := os.Stat(s.path)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.Users = raw.Users
	if s.Users == nil {
		s.Users = make(map[string]*User)
	}
	// 填充 Username 字段（YAML map 的 key）。
	for username, u := range s.Users {
		u.Username = username
	}
	if info != nil {
		s.modTime = info.ModTime()
	}
	return nil
}

// HashPassword 生成 bcrypt hash。
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

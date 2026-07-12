package auth

import (
	"context"
	"fmt"

	"golang.org/x/crypto/bcrypt"

	runtimestore "oops/internal/store/runtime"
)

// User 表示可登录的用户。
type User struct {
	Username string
	Name     string
	Password string // bcrypt hash
}

// Store 管理单用户凭证，内存缓存 + SQLite 持久化。
type Store struct {
	User    *User
	runtime *runtimestore.Store
}

// NewStoreFromRuntime loads the user from SQLite.
func NewStoreFromRuntime(runtime *runtimestore.Store) (*Store, error) {
	if runtime == nil {
		return nil, fmt.Errorf("runtime store is required")
	}
	u, err := runtime.GetUser(context.Background())
	if err != nil {
		return &Store{runtime: runtime}, nil
	}
	return &Store{
		User:    &User{Username: u.Username, Name: u.Name, Password: u.Password},
		runtime: runtime,
	}, nil
}

// IsSetup 返回是否已完成初始账户设置。
func (s *Store) IsSetup() bool {
	return s.User != nil
}

// Setup 创建初始管理员账户并持久化到 SQLite。
func (s *Store) Setup(username, name, password string) error {
	if s.IsSetup() {
		return fmt.Errorf("user already configured")
	}
	if s.runtime == nil {
		return fmt.Errorf("runtime store not available")
	}
	if username == "" || password == "" {
		return fmt.Errorf("username and password cannot be empty")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	u := &User{
		Username: username,
		Name:     name,
		Password: string(hash),
	}

	if err := s.runtime.UpsertUser(context.Background(), runtimestore.UserRecord{
		Username: u.Username,
		Name:     u.Name,
		Password: u.Password,
	}); err != nil {
		return fmt.Errorf("persist user: %w", err)
	}

	s.User = u
	return nil
}

// Validate 验证用户名和密码。
func (s *Store) Validate(username, password string) (*User, error) {
	if s.User == nil {
		return nil, fmt.Errorf("no user configured")
	}
	if username != s.User.Username {
		return nil, fmt.Errorf("invalid username or password")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(s.User.Password), []byte(password)); err != nil {
		return nil, fmt.Errorf("invalid username or password")
	}
	return s.User, nil
}

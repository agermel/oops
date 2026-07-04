package auth

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// DefaultPath is the default path for the user credentials YAML file.
const DefaultPath = "data/users.yml"

// User 表示可登录的用户。
type User struct {
	Username string `yaml:"username"`
	Name     string `yaml:"name"`
	Password string `yaml:"password"` // bcrypt hash
}

// Store 管理单用户凭证。
type Store struct {
	User *User
	path string // YAML 文件路径，用于 Setup 时回写
}

// NewStore 从 YAML 文件加载用户。文件不存在时返回空 Store（未初始化状态），
// 由前端 Web 页面完成账户设置，不再阻塞在 stdin 交互式提示。
func NewStore(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Store{path: path}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read users.yml: %w", err)
	}

	var u User
	if err := yaml.Unmarshal(data, &u); err != nil {
		return nil, fmt.Errorf("parse users.yml: %w", err)
	}
	if u.Username == "" || u.Password == "" {
		return nil, fmt.Errorf("users.yml: username and password are required")
	}
	return &Store{User: &u, path: path}, nil
}

// IsSetup 返回是否已完成初始账户设置。
func (s *Store) IsSetup() bool {
	return s.User != nil
}

// Setup 创建初始管理员账户，哈希密码并持久化到 YAML 文件。
// 仅在尚未设置时可用。
func (s *Store) Setup(username, name, password string) error {
	if s.IsSetup() {
		return fmt.Errorf("user already configured")
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

	data, err := yaml.Marshal(u)
	if err != nil {
		return fmt.Errorf("marshal user: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("write users.yml: %w", err)
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

// promptAndSave 交互式创建用户并写入 YAML。
func promptAndSave(path string) (*Store, error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Username: ")
	username, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read username: %w", err)
	}
	username = strings.TrimSpace(username)

	fmt.Print("Name: ")
	name, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read name: %w", err)
	}
	name = strings.TrimSpace(name)

	fmt.Print("Password: ")
	password, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read password: %w", err)
	}
	password = strings.TrimSpace(password)

	if username == "" || password == "" {
		return nil, fmt.Errorf("username and password cannot be empty")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	u := &User{
		Username: username,
		Name:     name,
		Password: string(hash),
	}

	data, err := yaml.Marshal(u)
	if err != nil {
		return nil, fmt.Errorf("marshal user: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, fmt.Errorf("write users.yml: %w", err)
	}

	fmt.Fprintf(os.Stderr, "User %q created and saved to %s\n", username, path)
	return &Store{User: u}, nil
}

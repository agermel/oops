package config

import (
	"errors"
	"os"
	"strings"

	"github.com/spf13/viper"
)

const (
	// EnvPath 是覆盖配置文件路径的环境变量。
	EnvPath = "OOPS_CONFIG"

	// DefaultPath 是生产/本地默认配置文件路径。
	DefaultPath = "config/config.yaml"
)

// Config 是应用启动或刷新时读取到的完整配置。
type Config struct {
	LLM LLMConfig `mapstructure:"llm"`
}

// LLMConfig 保存 LLM Agent 配置。
type LLMConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Model   string `mapstructure:"model"`
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
}

// MCPConfig 保存一个 MCP Server 的连接配置。
//
// 社区 MCP Server（如 askdba/mysql-mcp-server）通常用 stdio 模式：
//
//	transport: "stdio"
//	command: "mysql-mcp-server"
//	args: ["--read-only"]
//	env: ["MYSQL_DSN=user:pass@tcp(...)"]
//
// 跨网络部署时用 sse 模式：
//
//	transport: "sse"
//	url: "http://10.0.0.1:19900/sse"
type MCPConfig struct {
	Enabled   bool     `mapstructure:"enabled"`
	Transport string   `mapstructure:"transport"` // "stdio" | "sse"
	Command   string   `mapstructure:"command"`   // stdio: 二进制路径
	Args      []string `mapstructure:"args"`      // stdio: 启动参数
	Env       []string `mapstructure:"env"`       // stdio: 环境变量
	URL       string   `mapstructure:"url"`       // sse: 端点地址
}

// Load 使用 Viper 读取指定 YAML 配置文件。
// 读取时会展开文件中的 ${ENV_VAR} 占位符。
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	expanded := os.ExpandEnv(string(data))

	v := viper.New()
	v.SetConfigType("yaml")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadConfig(strings.NewReader(expanded)); err != nil {
		return Config{}, err
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadRuntime 按运行时优先级读取配置。
func LoadRuntime() (Config, error) {
	if path := os.Getenv(EnvPath); path != "" {
		return Load(path)
	}

	cfg, err := Load(DefaultPath)
	if err == nil {
		return cfg, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Config{}, err
	}
	return Load("config/config.example.yaml")
}

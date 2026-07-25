package config

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const (
	// EnvPath 是覆盖配置文件路径的环境变量。
	EnvPath = "OOPS_CONFIG"

	// DefaultPath 是生产/本地默认配置文件路径。
	DefaultPath = "config/config.yaml"

	minRunTerminalBytes = 256
)

// Config 是应用启动或刷新时读取到的完整配置。
type Config struct {
	LLM  LLMConfig  `mapstructure:"llm"`
	Run  RunLimits  `mapstructure:"run"`
	HTTP HTTPConfig `mapstructure:"http"`
}

// LLMConfig 保存 LLM Agent 配置。
type LLMConfig struct {
	Enabled       bool     `mapstructure:"enabled"`
	Provider      string   `mapstructure:"provider"`
	Model         string   `mapstructure:"model"`
	BaseURL       string   `mapstructure:"base_url"`
	APIKey        string   `mapstructure:"api_key"`
	ContextWindow int      `mapstructure:"context_window"`
	MaxTokens     int      `mapstructure:"max_tokens"`
	Temperature   *float32 `mapstructure:"temperature"`
}

// HTTPConfig defines trust-boundary settings for HTTP listeners.
type HTTPConfig struct {
	TrustedProxyCIDRs []string `mapstructure:"trusted_proxy_cidrs"`
}

// RunLimits 定义 Run、SSE 重放和关闭的资源上限。
type RunLimits struct {
	MaxActiveRuns            int           `mapstructure:"max_active_runs"`
	MaxRetainedEvents        int           `mapstructure:"max_retained_events"`
	MaxRetainedBytes         int           `mapstructure:"max_retained_bytes"`
	MaxEventBytes            int           `mapstructure:"max_event_bytes"`
	MaxTerminalBytes         int           `mapstructure:"max_terminal_bytes"`
	MaxErrorTextBytes        int           `mapstructure:"max_error_text_bytes"`
	MaxSubscribers           int           `mapstructure:"max_subscribers"`
	MaxSubscriberQueueEvents int           `mapstructure:"max_subscriber_queue_events"`
	MaxSubscriberQueueBytes  int           `mapstructure:"max_subscriber_queue_bytes"`
	MaxLiveQueueBytes        int           `mapstructure:"max_live_queue_bytes"`
	CompletedTTL             time.Duration `mapstructure:"completed_ttl"`
	RetryAfter               time.Duration `mapstructure:"retry_after"`
	CloseTimeout             time.Duration `mapstructure:"close_timeout"`
}

// DefaultRunLimits returns the bounded production defaults for Run execution.
func DefaultRunLimits() RunLimits {
	return RunLimits{
		MaxActiveRuns:            32,
		MaxRetainedEvents:        512,
		MaxRetainedBytes:         4 << 20,
		MaxEventBytes:            256 << 10,
		MaxTerminalBytes:         8 << 10,
		MaxErrorTextBytes:        2 << 10,
		MaxSubscribers:           16,
		MaxSubscriberQueueEvents: 64,
		MaxSubscriberQueueBytes:  512 << 10,
		MaxLiveQueueBytes:        8 << 20,
		CompletedTTL:             15 * time.Minute,
		RetryAfter:               time.Second,
		CloseTimeout:             15 * time.Second,
	}
}

// WithDefaults fills omitted limits with production defaults.
func (l RunLimits) WithDefaults() RunLimits {
	d := DefaultRunLimits()
	if l.MaxActiveRuns <= 0 {
		l.MaxActiveRuns = d.MaxActiveRuns
	}
	if l.MaxRetainedEvents <= 0 {
		l.MaxRetainedEvents = d.MaxRetainedEvents
	}
	if l.MaxRetainedBytes <= 0 {
		l.MaxRetainedBytes = d.MaxRetainedBytes
	}
	if l.MaxEventBytes <= 0 {
		l.MaxEventBytes = d.MaxEventBytes
	}
	if l.MaxTerminalBytes < minRunTerminalBytes {
		if l.MaxTerminalBytes > 0 {
			l.MaxTerminalBytes = minRunTerminalBytes
		} else {
			l.MaxTerminalBytes = d.MaxTerminalBytes
		}
	}
	if l.MaxErrorTextBytes <= 0 {
		l.MaxErrorTextBytes = d.MaxErrorTextBytes
	}
	if l.MaxSubscribers <= 0 {
		l.MaxSubscribers = d.MaxSubscribers
	}
	if l.MaxSubscriberQueueEvents <= 0 {
		l.MaxSubscriberQueueEvents = d.MaxSubscriberQueueEvents
	}
	if l.MaxSubscriberQueueBytes <= 0 {
		l.MaxSubscriberQueueBytes = d.MaxSubscriberQueueBytes
	}
	if l.MaxLiveQueueBytes <= 0 {
		l.MaxLiveQueueBytes = d.MaxLiveQueueBytes
	}
	if l.CompletedTTL <= 0 {
		l.CompletedTTL = d.CompletedTTL
	}
	if l.RetryAfter <= 0 {
		l.RetryAfter = d.RetryAfter
	}
	if l.CloseTimeout <= 0 {
		l.CloseTimeout = d.CloseTimeout
	}
	return l
}

// MCPConfig 保存一个 MCP Server 的连接配置。
//
// 社区 MCP Server（如 askdba/mysql-mcp-server）通常用 stdio 模式：
//
//	transport: "stdio"
//	command: "mysql-mcp-server"
//	args: []
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

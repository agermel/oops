package config

import (
	"errors"
	"os"
	"strings"

	"oops/internal/connection"

	"github.com/spf13/viper"
)

const (
	// EnvPath 是覆盖配置文件路径的环境变量。
	EnvPath = "OOPS_CONFIG"

	// DefaultPath 是生产/本地默认配置文件路径。
	DefaultPath = "config/config.yaml"

	// ExamplePath 是默认配置缺失时的示例配置路径。
	ExamplePath = "config/config.example.yaml"
)

// Config 是应用启动或刷新时读取到的完整配置。
type Config struct {
	Env              string                  `mapstructure:"env"`
	Nodelets         []NodeletConfig         `mapstructure:"nodelets"`
	ExtraConnections []connection.Connection `mapstructure:"connections"`
	MySQL            MySQLConfig             `mapstructure:"mysql"`
	Redis            RedisConfig             `mapstructure:"redis"`
	Etcd             EtcdConfig              `mapstructure:"etcd"`
	Kafka            KafkaConfig             `mapstructure:"kafka"`
	OTel             OTelConfig              `mapstructure:"otel"`
	LLM              LLMConfig               `mapstructure:"llm"`
}

// NodeletConfig 保存一台 oops-nodelet 的访问地址。
type NodeletConfig struct {
	ID      string `mapstructure:"id" json:"id"`
	Name    string `mapstructure:"name" json:"name"`
	Address string `mapstructure:"address" json:"address"`
	Token   string `mapstructure:"token" json:"-"`
}

// MySQLConfig 保存 MySQL 连接配置。
type MySQLConfig struct {
	DSN string `mapstructure:"dsn"`
}

// RedisConfig 保存 Redis 连接配置。
type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
}

// EtcdConfig 保存 Etcd 连接配置。
type EtcdConfig struct {
	Endpoints []string `mapstructure:"endpoints"`
	Username  string   `mapstructure:"username"`
	Password  string   `mapstructure:"password"`
}

// KafkaConfig 保存 Kafka 连接配置。
type KafkaConfig struct {
	Addrs    []string `mapstructure:"addrs"`
	Username string   `mapstructure:"username"`
	Password string   `mapstructure:"password"`
}

// OTelConfig 保存 OpenTelemetry Collector 连接配置。
type OTelConfig struct {
	Endpoint string `mapstructure:"endpoint"`
}

// LLMConfig 保存 LLM Agent 配置。
type LLMConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Model   string `mapstructure:"model"`
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
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

// LoadDefault 使用默认路径读取配置。
func LoadDefault() (Config, error) {
	return Load(DefaultPath)
}

// LoadRuntime 按运行时优先级读取配置。
func LoadRuntime() (Config, error) {
	if path := os.Getenv(EnvPath); path != "" {
		return Load(path)
	}

	cfg, err := LoadDefault()
	if err == nil {
		return cfg, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Config{}, err
	}
	return Load(ExamplePath)
}

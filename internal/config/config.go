package config

import (
	"strings"

	"oops/internal/connection"

	"github.com/spf13/viper"
)

// DefaultPath 是生产/本地默认配置文件路径。
const DefaultPath = "config/config.yaml"

// Config 是应用启动或刷新时读取到的完整配置。
type Config struct {
	Env              string                  `mapstructure:"env"`
	Agents           []AgentConfig           `mapstructure:"agents"`
	ExtraConnections []connection.Connection `mapstructure:"connections"`
	MySQL            MySQLConfig             `mapstructure:"mysql"`
	Redis            RedisConfig             `mapstructure:"redis"`
	Etcd             EtcdConfig              `mapstructure:"etcd"`
	Kafka            KafkaConfig             `mapstructure:"kafka"`
	OTel             OTelConfig              `mapstructure:"otel"`
}

// AgentConfig 保存一台 oops-agent 的访问地址。
type AgentConfig struct {
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

// Load 使用 Viper 读取指定 YAML 配置文件。
func Load(path string) (Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
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

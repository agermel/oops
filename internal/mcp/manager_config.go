package mcp

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

// expandEnvSlice 展开字符串切片中的 ${VAR} 环境变量引用。
func expandEnvSlice(vals []string) []string {
	if len(vals) == 0 {
		return vals
	}
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = os.ExpandEnv(v)
	}
	return out
}

func normalizeConnectionConfig(cfg ConnectionConfig) ConnectionConfig {
	cfg.NodeletID = strings.TrimSpace(cfg.NodeletID)
	cfg.ContainerID = strings.TrimSpace(cfg.ContainerID)
	switch strings.ToLower(cfg.Type) {
	case "mysql":
		return normalizeEnvConfig(cfg, []string{
			"MYSQL_DSN",
		})
	case "redis":
		return normalizeRedisConnectionConfig(cfg)
	case "postgres":
		return normalizeEnvConfig(cfg, []string{
			"DATABASE_URL",
		})
	case "etcd":
		return normalizeEnvConfig(cfg, []string{
			"ETCD_ENDPOINTS",
			"ETCD_USERNAME",
			"ETCD_PASSWORD",
		})
	case "elasticsearch":
		return normalizeElasticsearchConnectionConfig(cfg)
	case "kafka":
		return normalizeKafkaConnectionConfig(cfg)
	case "nacos":
		return normalizeNacosConnectionConfig(cfg)
	default:
		return cfg
	}
}

func validateServerBoundConnection(cfg ConnectionConfig) error {
	if strings.TrimSpace(cfg.NodeletID) == "" {
		return fmt.Errorf("nodeletId is required")
	}
	return nil
}

func normalizeEnvConfig(cfg ConnectionConfig, keys []string) ConnectionConfig {
	generated := make([]string, 0, len(keys))
	for _, key := range keys {
		value := envValue(cfg.Env, key)
		if value != "" {
			generated = append(generated, key+"="+value)
		}
	}
	return withGeneratedEnv(cfg, generated, keys)
}

func normalizeRedisConnectionConfig(cfg ConnectionConfig) ConnectionConfig {
	generated := make([]string, 0, 5)
	if host := envValue(cfg.Env, "REDIS_HOST"); host != "" {
		generated = append(generated, "REDIS_HOST="+host)
	}
	if port := envValue(cfg.Env, "REDIS_PORT"); port != "" {
		generated = append(generated, "REDIS_PORT="+port)
	}
	if username := envValue(cfg.Env, "REDIS_USERNAME"); username != "" {
		generated = append(generated, "REDIS_USERNAME="+username)
	}
	if database := envValue(cfg.Env, "REDIS_DB"); database != "" {
		generated = append(generated, "REDIS_DB="+database)
	}
	if password := envValueAny(cfg.Env, "REDIS_PWD", "REDIS_PASSWORD"); password != "" {
		generated = append(generated, "REDIS_PWD="+password)
	}
	return withGeneratedEnv(cfg, generated, []string{
		"REDIS_HOST",
		"REDIS_PORT",
		"REDIS_USERNAME",
		"REDIS_DB",
		"REDIS_PWD",
		"REDIS_PASSWORD",
	})
}

func normalizeElasticsearchConnectionConfig(cfg ConnectionConfig) ConnectionConfig {
	hosts := envValueAny(cfg.Env, "ELASTICSEARCH_HOSTS", "ELASTICSEARCH_URL")
	generated := make([]string, 0, 3)
	if hosts != "" {
		generated = append(generated, "ELASTICSEARCH_HOSTS="+hosts)
	}
	if username := envValue(cfg.Env, "ELASTICSEARCH_USERNAME"); username != "" {
		generated = append(generated, "ELASTICSEARCH_USERNAME="+username)
	}
	if password := envValue(cfg.Env, "ELASTICSEARCH_PASSWORD"); password != "" {
		generated = append(generated, "ELASTICSEARCH_PASSWORD="+password)
	}
	return withGeneratedEnv(cfg, generated, []string{
		"ELASTICSEARCH_HOSTS",
		"ELASTICSEARCH_URL",
		"ELASTICSEARCH_USERNAME",
		"ELASTICSEARCH_PASSWORD",
	})
}

func normalizeKafkaConnectionConfig(cfg ConnectionConfig) ConnectionConfig {
	bootstrap := envValueAny(cfg.Env, "BOOTSTRAP_SERVERS", "KAFKA_BOOTSTRAP_SERVERS")
	username := envValueAny(cfg.Env, "KAFKA_API_KEY", "KAFKA_SASL_USERNAME")
	password := envValueAny(cfg.Env, "KAFKA_API_SECRET", "KAFKA_SASL_PASSWORD")
	securityProtocol := envValue(cfg.Env, "KAFKA_SECURITY_PROTOCOL")
	saslMechanism := envValueAny(cfg.Env, "KAFKA_SASL_MECHANISM", "KAFKA_SASL_MECHANISMS")

	if username != "" && password != "" {
		if securityProtocol == "" {
			securityProtocol = "sasl_plaintext"
		}
		if saslMechanism == "" {
			saslMechanism = "PLAIN"
		}
	}

	generated := make([]string, 0, 5)
	if bootstrap != "" {
		generated = append(generated, "BOOTSTRAP_SERVERS="+bootstrap)
	}
	if username != "" && password != "" {
		generated = append(generated, "KAFKA_API_KEY="+username)
		generated = append(generated, "KAFKA_API_SECRET="+password)
	}
	if securityProtocol != "" {
		generated = append(generated, "KAFKA_SECURITY_PROTOCOL="+securityProtocol)
	}
	if saslMechanism != "" {
		generated = append(generated, "KAFKA_SASL_MECHANISM="+saslMechanism)
	}
	return withGeneratedEnv(cfg, generated, []string{
		"BOOTSTRAP_SERVERS",
		"KAFKA_BOOTSTRAP_SERVERS",
		"KAFKA_API_KEY",
		"KAFKA_API_SECRET",
		"KAFKA_SASL_USERNAME",
		"KAFKA_SASL_PASSWORD",
		"KAFKA_SECURITY_PROTOCOL",
		"KAFKA_SASL_MECHANISM",
		"KAFKA_SASL_MECHANISMS",
	})
}

func normalizeNacosConnectionConfig(cfg ConnectionConfig) ConnectionConfig {
	addr := envValue(cfg.Env, "NACOS_ADDR")
	if addr == "" {
		addr = joinHostPort(argValue(cfg.Args, "--host"), argValue(cfg.Args, "--port"))
	}

	generated := make([]string, 0, 4)
	if addr != "" {
		generated = append(generated, "NACOS_ADDR="+addr)
	}
	if username := envValue(cfg.Env, "NACOS_USERNAME"); username != "" {
		generated = append(generated, "NACOS_USERNAME="+username)
	}
	if password := envValue(cfg.Env, "NACOS_PASSWORD"); password != "" {
		generated = append(generated, "NACOS_PASSWORD="+password)
	}
	if namespace := envValue(cfg.Env, "NACOS_NAMESPACE"); namespace != "" {
		generated = append(generated, "NACOS_NAMESPACE="+namespace)
	}

	cfg.Args = stripArgsWithValues(cfg.Args, []string{"--host", "--port", "--access_token"})
	return withGeneratedEnv(cfg, generated, []string{
		"NACOS_ADDR",
		"NACOS_USERNAME",
		"NACOS_PASSWORD",
		"NACOS_NAMESPACE",
	})
}

func withGeneratedEnv(cfg ConnectionConfig, generated []string, keys []string) ConnectionConfig {
	keySet := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		keySet[key] = struct{}{}
	}
	extra := make([]string, 0, len(cfg.Env))
	for _, item := range cfg.Env {
		if _, ok := keySet[envKey(item)]; ok {
			continue
		}
		extra = append(extra, item)
	}
	next := append(append([]string{}, generated...), extra...)
	if slices.Equal(cfg.Env, next) {
		return cfg
	}
	cfg.Env = next
	return cfg
}

func envKey(env string) string {
	key, _, _ := strings.Cut(env, "=")
	return strings.TrimSpace(key)
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(item, prefix))
		}
	}
	return ""
}

func envValueAny(env []string, keys ...string) string {
	for _, key := range keys {
		if value := envValue(env, key); value != "" {
			return value
		}
	}
	return ""
}

func argValue(args []string, flag string) string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func joinHostPort(host, port string) string {
	if host == "" {
		return ""
	}
	if port == "" || strings.Contains(host, ":") || strings.Contains(host, ",") || strings.Contains(host, "://") {
		return host
	}
	return host + ":" + port
}

func stripArgsWithValues(args []string, flags []string) []string {
	flagSet := make(map[string]struct{}, len(flags))
	for _, flag := range flags {
		flagSet[flag] = struct{}{}
	}
	kept := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if _, ok := flagSet[args[i]]; ok {
			i++
			continue
		}
		kept = append(kept, args[i])
	}
	return kept
}

package common

import "os"

// EnvOrDefault 读取环境变量，空值时使用默认值。
func EnvOrDefault(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

package main

import (
	"errors"
	"log"
	"net/http"
	"os"

	"oops/internal/api"
	"oops/internal/config"
)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	server := api.NewFromConfig(cfg, "web/dist")

	addr := envOrDefault("OOPS_ADDR", ":8081")
	log.Printf("ops plane API listening on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, server.Routes()))
}

// loadConfig 优先读取 config/config.yaml，缺失时读取示例配置。
func loadConfig() (config.Config, error) {
	if path := os.Getenv("OOPS_CONFIG"); path != "" {
		return config.Load(path)
	}

	cfg, err := config.LoadDefault()
	if err == nil {
		return cfg, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return config.Config{}, err
	}
	return config.Load("config/config.example.yaml")
}

// envOrDefault 读取环境变量，空值时使用默认值。
func envOrDefault(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

package main

import (
	"errors"
	"log"
	"net/http"
	"os"

	"oops/internal/api"
	"oops/internal/common"
	"oops/internal/config"
)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	// 组装 HTTP 服务
	server := api.NewFromConfig(cfg, "web/dist")

	addr := common.EnvOrDefault("OOPS_ADDR", ":8081")
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

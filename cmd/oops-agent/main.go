package main

import (
	"log"
	"net/http"
	"os"

	"oops/internal/agent"
	"oops/internal/docker"
)

// 子服务器的 agent 进程
func main() {
	addr := envOrDefault("OOPS_AGENT_ADDR", ":8686")
	publicAddress := envOrDefault("OOPS_AGENT_PUBLIC_ADDRESS", "http://localhost"+addr)
	token := os.Getenv("OOPS_AGENT_TOKEN")

	dockerClient, err := docker.NewClient(publicAddress)
	if err != nil {
		log.Fatal(err)
	}

	server := agent.NewServerWithToken(dockerClient, token)

	log.Printf("oops agent listening on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, server.Routes()))
}

// envOrDefault 读取环境变量，空值时使用默认值。
func envOrDefault(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

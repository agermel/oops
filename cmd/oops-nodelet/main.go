package main

import (
	"net/http"
	"os"
	"time"

	"oops/internal/common"
	"oops/internal/docker"
	"oops/internal/logutil"
	"oops/internal/nodelet"
)

// 子服务器的 nodelet 进程
func main() {
	addr := common.EnvOrDefault("OOPS_NODELET_ADDR", ":8686")
	publicAddress := common.EnvOrDefault("OOPS_NODELET_PUBLIC_ADDRESS", "http://localhost"+addr)
	token := os.Getenv("OOPS_NODELET_TOKEN")

	dockerClient, err := docker.NewClient(publicAddress)
	if err != nil {
		logutil.Fatalf("docker client: %v", err)
	}

	server := nodelet.NewServerWithToken(dockerClient, token)

	httpServer := &http.Server{
		Addr:         addr,
		Handler:      server.Routes(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	logutil.Infof("oops nodelet listening on http://localhost%s", addr)
	logutil.Fatalf("server: %v", httpServer.ListenAndServe())
}

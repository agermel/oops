package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"oops/internal/common"
	"oops/internal/docker"
	"oops/internal/logutil"
	"oops/internal/nodelet"
)

// 子服务器的 nodelet 进程
func main() {
	logPath := common.EnvOrDefault("OOPS_NODELET_LOG_PATH", "/var/log/oops/nodelet.log")
	logutil.Init(false, nil, logPath)

	addr := common.EnvOrDefault("OOPS_NODELET_ADDR", ":8686")
	publicAddress := common.EnvOrDefault("OOPS_NODELET_PUBLIC_ADDRESS", "http://localhost"+addr)
	token := os.Getenv("OOPS_NODELET_TOKEN")

	if token == "" {
		logutil.Fatalf("OOPS_NODELET_TOKEN must be set")
	}

	dockerClient, err := docker.NewClient(publicAddress)
	if err != nil {
		logutil.Fatalf("docker client: %v", err)
	}

	server := nodelet.NewServerWithToken(dockerClient, token)
	defer server.Shutdown()

	httpServer := &http.Server{
		Addr:         addr,
		Handler:      server.Routes(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 优雅关闭
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logutil.Infof("oops nodelet listening on http://localhost%s", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logutil.Fatalf("server: %v", err)
		}
	}()

	<-ctx.Done()
	logutil.Infof("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logutil.Fatalf("shutdown: %v", err)
	}
	logutil.Infof("stopped")
}

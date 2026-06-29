package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"oops/internal/common"
	"oops/internal/docker"
	"oops/internal/logutil"
	"oops/internal/nodelet"

	"go.uber.org/zap"
)

// resolveToken 获取或自动生成配对 token。
//
// 首次启动时生成随机 token 并持久化到文件，后续启动直接读文件。
// token 文件路径可通过 OOPS_NODELET_TOKEN_FILE 指定（默认 /var/lib/oops/nodelet/token）。
func resolveToken() string {
	tokenFile := common.EnvOrDefault("OOPS_NODELET_TOKEN_FILE", "/var/lib/oops/nodelet/token")

	// 从持久化文件读取。
	if data, err := os.ReadFile(tokenFile); err == nil && len(data) > 0 {
		return string(data)
	}

	// 首次启动：生成并持久化。
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		logutil.Fatalf("generate token: %v", err)
	}
	token := hex.EncodeToString(b[:])

	if err := os.MkdirAll(filepath.Dir(tokenFile), 0700); err != nil {
		logutil.Fatalf("create token dir: %v", err)
	}
	if err := os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
		logutil.Fatalf("write token file: %v", err)
	}

	fmt.Println("==============================================")
	fmt.Println("  Nodelet token (auto-generated, saved to disk)")
	fmt.Println("  " + token)
	fmt.Println("==============================================")

	return token
}

// 子服务器的 nodelet 进程
func main() {
	logPath := common.EnvOrDefault("OOPS_NODELET_LOG_PATH", "/var/log/oops/nodelet.log")
	logutil.Init(false, nil, logPath)

	addr := common.EnvOrDefault("OOPS_NODELET_ADDR", ":8686")
	publicAddress := common.EnvOrDefault("OOPS_NODELET_PUBLIC_ADDRESS", "http://localhost"+addr)

	token := resolveToken()

	dockerClient, err := docker.NewClient(publicAddress)
	if err != nil {
		logutil.Fatalf("docker client: %v", err)
	}

	server := nodelet.NewServerWithToken(dockerClient, token)
	defer server.Shutdown()

	tokenStatus := "configured"
	if token == "" {
		tokenStatus = "not configured"
	}
	logutil.Info("nodelet: starting",
		zap.String("token", tokenStatus),
		zap.String("provider", "docker"),
		zap.String("addr", addr),
		zap.String("publicAddress", publicAddress),
		zap.String("logPath", logPath),
	)

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

package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"oops/internal/api"
	"oops/internal/auth"
	"oops/internal/common"
	"oops/internal/config"
	"oops/internal/console"
	"oops/internal/logutil"
	"oops/internal/web"

	"go.uber.org/zap/zapcore"
)

func main() {
	// hash-password 子命令。
	if len(os.Args) > 1 && os.Args[1] == "hash-password" {
		fmt.Print("Password: ")
		var password string
		fmt.Scanln(&password)
		hash, err := auth.HashPassword(password)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(hash)
		return
	}
	// 初始化结构化日志：同时输出到 stderr 和 web 控制台 hub。
	logutil.Init(true, zapcore.AddSync(console.Default()))

	cfg, err := config.LoadRuntime()
	if err != nil {
		logutil.Fatalf("load config: %v", err)
	}

	// 组装 HTTP 服务
	server := api.NewFromConfig(cfg)
	mux := http.NewServeMux()
	server.Mount(mux)
	handler := web.MountStatic(mux, "web/dist", server.UserStore, server.TokenService)

	addr := common.EnvOrDefault("OOPS_ADDR", ":8081")

	httpServer := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	logutil.Infof("ops plane API listening on http://localhost%s", addr)
	logutil.Fatalf("server: %v", httpServer.ListenAndServe())
}

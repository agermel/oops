package main

import (
	"net/http"
	"time"

	"oops/internal/api"
	"oops/internal/common"
	"oops/internal/config"
	"oops/internal/console"
	"oops/internal/logutil"
	"oops/internal/web"

	"go.uber.org/zap/zapcore"
)

func main() {
	// 初始化结构化日志：同时输出到 stderr 和 web 控制台 hub。
	logutil.Init(true, zapcore.AddSync(console.Default()), "")

	cfg, err := config.LoadRuntime()
	if err != nil {
		logutil.Fatalf("load config: %v", err)
	}

	// 组装 HTTP 服务
	server := api.NewFromConfig(cfg)
	mux := http.NewServeMux()
	server.Mount(mux)
	handler := web.MountStatic(mux, "web/dist", server.TokenService, server.UserStore)

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

package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"oops/internal/api"
	"oops/internal/config"
	"oops/internal/console"
	"oops/internal/logutil"
	"oops/internal/web"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	// 初始化结构化日志：同时输出到 stderr 和 web 控制台 hub。
	consoleHub := console.NewHub()
	logutil.Init(true, zapcore.AddSync(consoleHub), "")

	cfg, err := config.LoadRuntime()
	if err != nil {
		consoleHub.Close()
		logutil.Fatalf("load config: %v", err)
	}

	// 组装 HTTP 服务
	server, err := api.NewFromConfigWithConsoleHub(cfg, consoleHub)
	if err != nil {
		consoleHub.Close()
		logutil.Fatalf("create API server: %v", err)
	}
	mux := http.NewServeMux()
	server.Mount(mux)
	handler := web.MountStatic(mux, "web/dist", server.TokenService, server.UserStore)

	addr := os.Getenv("OOPS_ADDR")
	if addr == "" {
		addr = ":8081"
	}

	httpServer := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	shutdownSignal, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- httpServer.ListenAndServe()
	}()

	logutil.Infof("ops plane API listening on http://localhost%s", addr)
	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			closeCtx, cancelClose := context.WithTimeout(context.Background(), cfg.Run.WithDefaults().CloseTimeout)
			closeErr := server.Close(closeCtx)
			cancelClose()
			if closeErr != nil {
				logutil.Error("close API server", zap.Error(closeErr))
			}
			closeConsoleAfterServerDrain(consoleHub, closeErr)
			return
		}
		httpCloseErr, closeErr := shutdownAPIServer(httpServer, server, cfg.Run.WithDefaults().CloseTimeout)
		if httpCloseErr != nil && !errors.Is(httpCloseErr, http.ErrServerClosed) {
			logutil.Error("shutdown HTTP server", zap.Error(httpCloseErr))
		}
		if closeErr != nil {
			logutil.Error("close API server", zap.Error(closeErr))
		}
		closeConsoleAfterServerDrain(consoleHub, closeErr)
		logutil.Fatalf("server: %v", err)
	case <-shutdownSignal.Done():
		httpCloseErr, closeErr := shutdownAPIServer(httpServer, server, cfg.Run.WithDefaults().CloseTimeout)
		if httpCloseErr != nil && !errors.Is(httpCloseErr, http.ErrServerClosed) {
			logutil.Error("shutdown HTTP server", zap.Error(httpCloseErr))
		}
		if closeErr != nil {
			logutil.Error("close API server", zap.Error(closeErr))
		}
		closeConsoleAfterServerDrain(consoleHub, closeErr)
	}
}

// closeConsoleAfterServerDrain preserves the process close deadline. A timed
// out drain leaves the hub open until process exit; the OS then reclaims it.
func closeConsoleAfterServerDrain(consoleHub *console.Hub, closeErr error) {
	if consoleHub != nil && closeErr == nil {
		consoleHub.Close()
	}
}

// shutdownAPIServer quiesces API work, drains HTTP handlers, and gives the
// server-owned resources an independent close budget. A timed-out Shutdown
// leaves active connections open, so Close forces those connections down
// before Server.Close waits for their owners.
func shutdownAPIServer(httpServer *http.Server, server *api.Server, timeout time.Duration) (error, error) {
	server.Quiesce()

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), timeout)
	httpErr := httpServer.Shutdown(shutdownCtx)
	cancelShutdown()
	if httpErr != nil {
		if forceErr := httpServer.Close(); forceErr != nil && !errors.Is(forceErr, http.ErrServerClosed) {
			httpErr = errors.Join(httpErr, forceErr)
		}
	}

	closeCtx, cancelClose := context.WithTimeout(context.Background(), timeout)
	defer cancelClose()
	return httpErr, server.Close(closeCtx)
}

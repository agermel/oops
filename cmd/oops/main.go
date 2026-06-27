package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"oops/internal/config"
	"oops/internal/connection"
	"oops/internal/connection/checker"
)

// statusItem 是 GUI 状态接口返回的一行连接状态。
type statusItem struct {
	Connection connection.Connection `json:"connection"`
	Result     connection.Result     `json:"result"`
	Error      string                `json:"error,omitempty"`
}

// server 保存 API 服务运行所需的配置和检查器。
type server struct {
	connections []connection.Connection
	registry    *connection.Registry
	staticDir   string
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	app := &server{
		connections: cfg.Connections(),
		registry:    checker.NewDefaultRegistry(),
		staticDir:   "web/dist",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/connections/status", app.handleConnectionStatus)
	mux.HandleFunc("/", app.handleStatic)

	addr := ":8080"
	log.Printf("ops plane API listening on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

// loadConfig 优先读取 config/config.yaml，缺失时读取示例配置。
func loadConfig() (config.Config, error) {
	cfg, err := config.LoadDefault()
	if err == nil {
		return cfg, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return config.Config{}, err
	}
	return config.Load("config/config.example.yaml")
}

// handleConnectionStatus 执行所有连接检查并返回 JSON。
func (s *server) handleConnectionStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	items := make([]statusItem, 0, len(s.connections))
	results := make([]statusItem, len(s.connections))

	var wg sync.WaitGroup
	for index, conn := range s.connections {
		wg.Add(1)
		go func(index int, conn connection.Connection) {
			defer wg.Done()

			// 每个连接独立超时，避免慢组件拖累其他组件的结果。
			checkCtx, checkCancel := context.WithTimeout(ctx, 6*time.Second)
			defer checkCancel()

			result, err := s.registry.Check(checkCtx, conn)
			item := statusItem{
				Connection: conn,
				Result:     result,
			}
			if err != nil {
				item.Error = err.Error()
				item.Result = connection.Result{
					ConnectionID: conn.ID,
					Status:       connection.StatusUnknown,
					Message:      err.Error(),
					CheckedAt:    time.Now(),
				}
			}
			results[index] = item
		}(index, conn)
	}
	wg.Wait()

	items = append(items, results...)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(items)
}

// handleStatic 在生产模式下提供 React 构建产物。
func (s *server) handleStatic(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(s.staticDir, filepath.Clean(r.URL.Path))
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		http.ServeFile(w, r, path)
		return
	}

	indexPath := filepath.Join(s.staticDir, "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		http.Error(w, "React build is missing. Run: npm --prefix web install && npm --prefix web run build", http.StatusServiceUnavailable)
		return
	}
	http.ServeFile(w, r, indexPath)
}

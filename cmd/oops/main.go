package main

import (
	"log"
	"net/http"
	"time"

	"oops/internal/api"
	"oops/internal/common"
	"oops/internal/config"
	"oops/internal/web"
)

func main() {
	cfg, err := config.LoadRuntime()
	if err != nil {
		log.Fatal(err)
	}

	// 组装 HTTP 服务
	server := api.NewFromConfig(cfg)
	mux := http.NewServeMux()
	server.Mount(mux)
	web.MountStatic(mux, "web/dist")

	addr := common.EnvOrDefault("OOPS_ADDR", ":8081")

	httpServer := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("ops plane API listening on http://localhost%s", addr)
	log.Fatal(httpServer.ListenAndServe())
}

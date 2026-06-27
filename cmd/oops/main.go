package main

import (
	"log"
	"net/http"

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
	log.Printf("ops plane API listening on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

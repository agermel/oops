package main

import (
	"log"
	"net/http"
	"os"

	"oops/internal/common"
	"oops/internal/docker"
	"oops/internal/nodelet"
)

// 子服务器的 nodelet 进程
func main() {
	addr := common.EnvOrDefault("OOPS_NODELET_ADDR", ":8686")
	publicAddress := common.EnvOrDefault("OOPS_NODELET_PUBLIC_ADDRESS", "http://localhost"+addr)
	token := os.Getenv("OOPS_NODELET_TOKEN")

	dockerClient, err := docker.NewClient(publicAddress)
	if err != nil {
		log.Fatal(err)
	}

	server := nodelet.NewServerWithToken(dockerClient, token)

	log.Printf("oops nodelet listening on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, server.Routes()))
}

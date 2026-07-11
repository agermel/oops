// etcd-mcp-server exposes etcd v3 key-value operations through MCP tools.
package main

import (
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/server"
)

func main() {
	s := server.NewMCPServer(
		"etcd-mcp-server",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	registerHealth(s)
	registerGet(s)
	registerPut(s)
	registerDelete(s)
	registerList(s)
	registerWatch(s)
	registerStatus(s)
	registerLease(s)

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "etcd-mcp-server: %v\n", err)
		os.Exit(1)
	}
}

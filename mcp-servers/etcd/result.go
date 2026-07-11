package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

func textResult(format string, args ...any) *mcp.CallToolResult {
	return mcp.NewToolResultText(fmt.Sprintf(format, args...))
}

func errorResult(format string, args ...any) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf(format, args...))
}

func jsonResult(value any) (*mcp.CallToolResult, error) {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errorResult("json marshal: %v", err), nil
	}
	return mcp.NewToolResultText(string(payload)), nil
}

func ctxWithTimeout(parent context.Context, seconds int) (context.Context, context.CancelFunc) {
	if seconds <= 0 {
		seconds = 10
	}
	return context.WithTimeout(parent, time.Duration(seconds)*time.Second)
}

func decodeArgs(raw any, target any) error {
	payload, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, target)
}

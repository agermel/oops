package runtime

import (
	"os"
	"path/filepath"
	"time"

	toolruntime "oops/internal/agent/core"
)

const (
	DefaultMaxLines       = 2000
	DefaultMaxBytes       = 50 * 1024
	DefaultMaxResults     = 200
	DefaultCommandTimeout = 30 * time.Second
)

type Options struct {
	Root           string
	MaxLines       int
	MaxBytes       int
	MaxResults     int
	CommandTimeout time.Duration
}

type workspaceToolConfig struct {
	root           string
	maxLines       int
	maxBytes       int
	maxResults     int
	commandTimeout time.Duration
}

func NewWorkspaceTools(options Options) ([]toolruntime.Tool, error) {
	cfg, err := normalizeOptions(options)
	if err != nil {
		return nil, err
	}
	return []toolruntime.Tool{
		newReadTool(cfg),
		newLSTool(cfg),
		newGrepTool(cfg),
		newFindTool(cfg),
		newBashTool(cfg),
		newWriteTool(cfg),
		newEditTool(cfg),
	}, nil
}

func normalizeOptions(options Options) (workspaceToolConfig, error) {
	root := options.Root
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return workspaceToolConfig{}, err
		}
		root = wd
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return workspaceToolConfig{}, err
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return workspaceToolConfig{}, err
	}
	cfg := workspaceToolConfig{
		root:           filepath.Clean(realRoot),
		maxLines:       firstPositive(options.MaxLines, DefaultMaxLines),
		maxBytes:       firstPositive(options.MaxBytes, DefaultMaxBytes),
		maxResults:     firstPositive(options.MaxResults, DefaultMaxResults),
		commandTimeout: options.CommandTimeout,
	}
	if cfg.commandTimeout <= 0 {
		cfg.commandTimeout = DefaultCommandTimeout
	}
	return cfg, nil
}

func firstPositive(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

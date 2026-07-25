package auth

import (
	"os"
	"strings"
)

type defaultContext struct{}

// DefaultContext reads ambient configuration from the host process.
func DefaultContext() AuthContext {
	return defaultContext{}
}

func (defaultContext) Env(name string) (string, bool) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return "", false
	}
	return value, true
}

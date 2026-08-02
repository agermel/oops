package workspace

import "errors"

var errStopWalk = errors.New("stop walk")

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", "node_modules", ".cache", "dist", "build":
		return true
	default:
		return false
	}
}

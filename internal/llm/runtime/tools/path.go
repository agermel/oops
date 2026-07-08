package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type resolvedPath struct {
	abs string
	rel string
}

func (c config) resolveInside(input string) (resolvedPath, error) {
	if input == "" {
		input = "."
	}
	target := input
	if !filepath.IsAbs(target) {
		target = filepath.Join(c.root, target)
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return resolvedPath{}, err
	}
	canonical, err := resolveCanonical(filepath.Clean(abs))
	if err != nil {
		return resolvedPath{}, err
	}
	rel, err := filepath.Rel(c.root, canonical)
	if err != nil {
		return resolvedPath{}, err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return resolvedPath{}, fmt.Errorf("path escapes workspace: %s", input)
	}
	if rel == "." {
		rel = ""
	}
	return resolvedPath{abs: canonical, rel: filepath.ToSlash(rel)}, nil
}

func (c config) ensureInsideExisting(path string) error {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(c.root, canonical)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("path escapes workspace: %s", path)
	}
	return nil
}

func resolveCanonical(path string) (string, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Clean(canonical), nil
	}
	if !isMissingPath(err) {
		return "", err
	}
	parent := filepath.Dir(path)
	for {
		parentCanonical, parentErr := filepath.EvalSymlinks(parent)
		if parentErr == nil {
			rel, relErr := filepath.Rel(parent, path)
			if relErr != nil {
				return "", relErr
			}
			return filepath.Clean(filepath.Join(parentCanonical, rel)), nil
		}
		if !isMissingPath(parentErr) {
			return "", parentErr
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", err
		}
		parent = next
	}
}

func isMissingPath(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}

func relativeOrDot(path resolvedPath) string {
	if path.rel == "" {
		return "."
	}
	return path.rel
}

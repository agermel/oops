package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type FileStorage struct {
	dir string
}

const maxSessionIDLength = 128

func NewFileStorage(dir string) (*FileStorage, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	return &FileStorage{dir: root}, nil
}

func (s *FileStorage) Append(sessionID string, entry Entry) error {
	name, err := sessionFilename(sessionID)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := openAppendFile(root, name)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

func (s *FileStorage) Load(sessionID string) ([]Entry, error) {
	name, err := sessionFilename(sessionID)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	exact, err := hasExactEntry(root, name)
	if err != nil {
		return nil, err
	}
	if !exact {
		return nil, nil
	}
	file, err := root.Open(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	entries := []Entry{}
	candidate := &Session{id: sessionID, entries: make(map[string]Entry)}
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var entry Entry
		if err := json.Unmarshal([]byte(text), &entry); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", name, line, err)
		}
		if err := candidate.loadEntryLocked(entry); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", name, line, err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%s:%d: %w", name, line, err)
	}
	return entries, nil
}

func (s *FileStorage) List() ([]string, error) {
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		if !validSessionID(id) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *FileStorage) Delete(sessionID string) (bool, error) {
	name, err := sessionFilename(sessionID)
	if err != nil {
		return false, err
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return false, err
	}
	defer root.Close()
	exact, err := hasExactEntry(root, name)
	if err != nil {
		return false, err
	}
	if !exact {
		return false, nil
	}
	if err := root.Remove(name); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func openAppendFile(root *os.Root, name string) (*os.File, error) {
	exact, err := hasExactEntry(root, name)
	if err != nil {
		return nil, err
	}
	if exact {
		return root.OpenFile(name, os.O_APPEND|os.O_WRONLY, 0644)
	}

	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if !os.IsExist(err) {
			return nil, err
		}
		exact, inspectErr := hasExactEntry(root, name)
		if inspectErr != nil {
			return nil, inspectErr
		}
		if !exact {
			return nil, fmt.Errorf("session filename conflicts with an existing directory entry")
		}
		return root.OpenFile(name, os.O_APPEND|os.O_WRONLY, 0644)
	}

	exact, inspectErr := hasExactEntry(root, name)
	if inspectErr == nil && exact {
		return file, nil
	}
	_ = file.Close()
	_ = root.Remove(name)
	if inspectErr != nil {
		return nil, inspectErr
	}
	return nil, fmt.Errorf("session filename was not created exactly")
}

func hasExactEntry(root *os.Root, name string) (bool, error) {
	dir, err := root.Open(".")
	if err != nil {
		return false, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Name() == name {
			return true, nil
		}
	}
	return false, nil
}

func sessionFilename(sessionID string) (string, error) {
	if err := ValidateID(sessionID); err != nil {
		return "", err
	}
	return sessionID + ".jsonl", nil
}

// ValidateID enforces the fixed alphabet accepted by session storage.
func ValidateID(sessionID string) error {
	if !validSessionID(sessionID) {
		return fmt.Errorf("invalid session id")
	}
	return nil
}

func validSessionID(sessionID string) bool {
	if len(sessionID) == 0 || len(sessionID) > maxSessionIDLength {
		return false
	}
	for i := range len(sessionID) {
		char := sessionID[i]
		if (char >= 'a' && char <= 'z') ||
			(char >= '0' && char <= '9') ||
			char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

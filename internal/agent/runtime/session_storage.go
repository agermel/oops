package runtime

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type FileStorage struct {
	dir string
}

const (
	maxSessionIDLength  = 128
	maxSessionLineBytes = 4 * 1024 * 1024
)

type corruptSessionReason string

const (
	corruptSessionInvalidJSON    corruptSessionReason = "invalid_json"
	corruptSessionInvalidEntry   corruptSessionReason = "invalid_entry"
	corruptSessionInvalidHistory corruptSessionReason = "invalid_history"
	corruptSessionLineTooLong    corruptSessionReason = "line_too_long"
)

var errSessionLineTooLong = errors.New("session line exceeds size limit")

type corruptSessionFileError struct {
	file   string
	line   int
	reason corruptSessionReason
	cause  error
}

type StorageCommitError struct {
	Operation string
	Phase     string
	Committed bool
	Err       error
}

func (e *StorageCommitError) Error() string {
	return fmt.Sprintf("%s %s: %v", e.Operation, e.Phase, e.Err)
}

func (e *StorageCommitError) Unwrap() error {
	return e.Err
}

func (e *corruptSessionFileError) Error() string {
	return fmt.Sprintf("%s:%d: %s", e.file, e.line, e.reason)
}

func (e *corruptSessionFileError) Unwrap() error {
	return e.cause
}

func newCorruptSessionFileError(file string, line int, reason corruptSessionReason, cause error) error {
	return &corruptSessionFileError{file: file, line: line, reason: reason, cause: cause}
}

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
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := rejectSessionNameConflict(root, name); err != nil {
		return err
	}
	tempName, file, err := createSessionTemp(root, name)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = root.Remove(tempName)
		}
	}()
	if err := copyExistingSession(root, name, file); err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := root.Rename(tempName, name); err != nil {
		return err
	}
	committed = true
	if err := syncRootDir(root); err != nil {
		return &StorageCommitError{Operation: "append", Phase: "directory-sync", Committed: true, Err: err}
	}
	return nil
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
	return decodeSessionEntries(file, name, sessionID)
}

func decodeSessionEntries(source io.Reader, name, sessionID string) ([]Entry, error) {
	reader := bufio.NewReaderSize(source, 64*1024)
	entries := []Entry{}
	candidate := &Session{id: sessionID, entries: make(map[string]Entry)}
	for line := 1; ; line++ {
		data, err := readSessionLine(reader)
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, errSessionLineTooLong) {
			return nil, newCorruptSessionFileError(name, line, corruptSessionLineTooLong, err)
		}
		if err != nil {
			return nil, fmt.Errorf("%s:%d: read session file: %w", name, line, err)
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			continue
		}
		var entry Entry
		if err := json.Unmarshal([]byte(text), &entry); err != nil {
			reason := corruptSessionInvalidEntry
			var syntaxErr *json.SyntaxError
			var typeErr *json.UnmarshalTypeError
			if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) {
				reason = corruptSessionInvalidJSON
			}
			return nil, newCorruptSessionFileError(name, line, reason, err)
		}
		if err := candidate.loadEntryLocked(entry); err != nil {
			return nil, newCorruptSessionFileError(name, line, corruptSessionInvalidHistory, err)
		}
		entries = append(entries, entry)
	}
	if err := candidate.validateLoadedHistoryLocked(); err != nil {
		return nil, newCorruptSessionFileError(name, len(entries), corruptSessionInvalidHistory, err)
	}
	return entries, nil
}

func readSessionLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if err != nil && !errors.Is(err, bufio.ErrBufferFull) && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if err == nil {
			fragment = fragment[:len(fragment)-1]
		}
		if len(line) > maxSessionLineBytes+1-len(fragment) {
			return nil, errSessionLineTooLong
		}
		line = append(line, fragment...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(line) == 0 {
			return nil, io.EOF
		}
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if len(line) > maxSessionLineBytes {
			return nil, errSessionLineTooLong
		}
		return line, nil
	}
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
	if err := syncRootDir(root); err != nil {
		return true, &StorageCommitError{Operation: "delete", Phase: "directory-sync", Committed: true, Err: err}
	}
	return true, nil
}

func createSessionTemp(root *os.Root, name string) (string, *os.File, error) {
	for i := 0; i < 100; i++ {
		tempName := fmt.Sprintf(".%s.tmp.%d.%d.%d", name, os.Getpid(), time.Now().UnixNano(), i)
		file, err := root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			return tempName, file, nil
		}
		if !os.IsExist(err) {
			return "", nil, err
		}
	}
	return "", nil, fmt.Errorf("create session temp file: too many collisions")
}

func copyExistingSession(root *os.Root, name string, target *os.File) error {
	exact, err := hasExactEntry(root, name)
	if err != nil {
		return err
	}
	if !exact {
		return nil
	}
	source, err := root.Open(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer source.Close()
	buffer := make([]byte, 32*1024)
	var last byte
	wrote := false
	for {
		n, readErr := source.Read(buffer)
		if n > 0 {
			chunk := buffer[:n]
			wrote = true
			last = chunk[len(chunk)-1]
			if _, err := target.Write(chunk); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if wrote && last != '\n' {
		if _, err := target.Write([]byte{'\n'}); err != nil {
			return err
		}
	}
	return nil
}

func syncRootDir(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
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

func rejectSessionNameConflict(root *os.Root, name string) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == name {
			return nil
		}
		if strings.EqualFold(entry.Name(), name) {
			return fmt.Errorf("session filename conflicts with an existing directory entry")
		}
	}
	return nil
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

package config

import (
	"context"
	"fmt"
	"strings"
	"sync"

	runtimestore "oops/internal/store/runtime"
)

// ContainerDSNStore persists user DSN overrides per container.
// Runtime writes go to SQLite.
type ContainerDSNStore struct {
	mu      sync.RWMutex
	runtime *runtimestore.Store
	entries map[string]map[string]string // key: "nodeletID/containerID", value: user KV overrides
}

// NewContainerDSNStoreWithRuntime loads DSN overrides from SQLite.
func NewContainerDSNStoreWithRuntime(runtime *runtimestore.Store) (*ContainerDSNStore, error) {
	if runtime == nil {
		return nil, fmt.Errorf("runtime store is required")
	}
	s := &ContainerDSNStore{
		runtime: runtime,
		entries: make(map[string]map[string]string),
	}
	if err := s.load(); err != nil {
		return nil, fmt.Errorf("load container dsn: %w", err)
	}
	return s, nil
}

// containerKey builds the internal key for a container.
func containerKey(nodeletID, containerID string) string {
	return nodeletID + "/" + containerID
}

// Get returns the user KV overrides for a container, or nil if none exist.
func (s *ContainerDSNStore) Get(nodeletID, containerID string) map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pairs, ok := s.entries[containerKey(nodeletID, containerID)]
	if !ok {
		return nil
	}
	// return a copy to avoid races
	out := make(map[string]string, len(pairs))
	for k, v := range pairs {
		out[k] = v
	}
	return out
}

// Set saves user KV overrides for a container.
func (s *ContainerDSNStore) Set(nodeletID, containerID string, pairs map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := containerKey(nodeletID, containerID)
	if len(pairs) == 0 {
		delete(s.entries, key)
	} else {
		// copy to avoid external mutation
		cp := make(map[string]string, len(pairs))
		for k, v := range pairs {
			cp[k] = v
		}
		s.entries[key] = cp
	}

	return s.saveLocked()
}

// Delete removes user KV overrides for a container (revert to detected).
func (s *ContainerDSNStore) Delete(nodeletID, containerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.entries, containerKey(nodeletID, containerID))
	return s.saveLocked()
}

func (s *ContainerDSNStore) load() error {
	ctx := context.Background()
	records, err := s.runtime.ListDSNRecords(ctx)
	if err != nil {
		return err
	}
	s.entries = dsnEntriesFromRuntime(records)
	if s.entries == nil {
		s.entries = make(map[string]map[string]string)
	}
	return nil
}

func (s *ContainerDSNStore) saveLocked() error {
	return s.runtime.ReplaceDSNRecords(context.Background(), dsnEntriesToRuntime(s.entries))
}

func dsnEntriesToRuntime(entries map[string]map[string]string) []runtimestore.DSNRecord {
	records := make([]runtimestore.DSNRecord, 0, len(entries))
	for key, pairs := range entries {
		nodeletID, containerID, ok := strings.Cut(key, "/")
		if !ok {
			continue
		}
		cp := make(map[string]string, len(pairs))
		for k, v := range pairs {
			cp[k] = v
		}
		records = append(records, runtimestore.DSNRecord{
			NodeletID:   nodeletID,
			ContainerID: containerID,
			Pairs:       cp,
		})
	}
	return records
}

func dsnEntriesFromRuntime(records []runtimestore.DSNRecord) map[string]map[string]string {
	entries := make(map[string]map[string]string, len(records))
	for _, record := range records {
		cp := make(map[string]string, len(record.Pairs))
		for k, v := range record.Pairs {
			cp[k] = v
		}
		entries[containerKey(record.NodeletID, record.ContainerID)] = cp
	}
	return entries
}

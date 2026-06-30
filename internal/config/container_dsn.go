package config

import (
	"fmt"
	"sync"

	"oops/internal/store"
)

// DefaultDSNPath is the default path for the container DSN overrides file.
const DefaultDSNPath = "config/container_dsn.json"

// ContainerDSNStore persists user DSN overrides per container.
// It follows the same load/save pattern as ProjectStore and mcp.Manager.
type ContainerDSNStore struct {
	mu      sync.RWMutex
	path    string
	entries map[string]map[string]string // key: "nodeletID/containerID", value: user KV overrides
}

// dsnStoreConfig is the on-disk structure of container_dsn.json.
type dsnStoreConfig struct {
	Entries map[string]map[string]string `json:"entries"`
}

// NewContainerDSNStore loads the container DSN store from path.
// If the file does not exist, an empty store is created.
func NewContainerDSNStore(path string) (*ContainerDSNStore, error) {
	s := &ContainerDSNStore{
		path:    path,
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
	var cfg dsnStoreConfig
	if err := store.LoadJSON(s.path, &cfg); err != nil {
		return err
	}
	s.entries = cfg.Entries
	if s.entries == nil {
		s.entries = make(map[string]map[string]string)
	}
	return nil
}

func (s *ContainerDSNStore) saveLocked() error {
	return store.SaveJSON(s.path, dsnStoreConfig{Entries: s.entries})
}

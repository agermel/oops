package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

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
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("load container dsn: %w", err)
		}
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
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var cfg dsnStoreConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	s.entries = cfg.Entries
	if s.entries == nil {
		s.entries = make(map[string]map[string]string)
	}
	return nil
}

func (s *ContainerDSNStore) saveLocked() error {
	cfg := dsnStoreConfig{Entries: s.entries}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0600); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

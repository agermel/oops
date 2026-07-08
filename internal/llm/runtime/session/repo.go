package session

import (
	"sync"
)

type Repository struct {
	mu       sync.RWMutex
	storage  Storage
	sessions map[string]*Session
}

func NewRepository(storage Storage) *Repository {
	return &Repository{
		storage:  storage,
		sessions: make(map[string]*Session),
	}
}

func (r *Repository) Create(id string) *Session {
	session := New(id)
	r.mu.Lock()
	r.sessions[session.ID()] = session
	r.mu.Unlock()
	return session
}

func (r *Repository) Get(id string) (*Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	session, ok := r.sessions[id]
	return session, ok
}

func (r *Repository) Load(id string) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if session, ok := r.sessions[id]; ok {
		return session, nil
	}
	session := New(id)
	if r.storage != nil {
		entries, err := r.storage.Load(id)
		if err != nil {
			return nil, err
		}
		if err := session.Load(entries); err != nil {
			return nil, err
		}
	}
	r.sessions[id] = session
	return session, nil
}

func (r *Repository) SaveEntry(sessionID string, entry Entry) error {
	if r.storage == nil {
		return nil
	}
	return r.storage.Append(sessionID, entry)
}

func (r *Repository) List() ([]Info, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.storage != nil {
		ids, err := r.storage.List()
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if _, ok := r.sessions[id]; ok {
				continue
			}
			session := New(id)
			entries, err := r.storage.Load(id)
			if err != nil {
				return nil, err
			}
			if err := session.Load(entries); err != nil {
				return nil, err
			}
			r.sessions[id] = session
		}
	}
	infos := make([]Info, 0, len(r.sessions))
	for _, session := range r.sessions {
		infos = append(infos, session.Info())
	}
	return infos, nil
}

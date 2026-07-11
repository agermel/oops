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

func (r *Repository) Put(session *Session) {
	if session == nil {
		return
	}
	r.mu.Lock()
	r.sessions[session.ID()] = session
	r.mu.Unlock()
}

func (r *Repository) Load(id string) (*Session, error) {
	r.mu.RLock()
	if session, ok := r.sessions[id]; ok {
		r.mu.RUnlock()
		return session, nil
	}
	storage := r.storage
	r.mu.RUnlock()

	candidate := New(id)
	if storage != nil {
		entries, err := storage.Load(id)
		if err != nil {
			return nil, err
		}
		if err := candidate.Load(entries); err != nil {
			return nil, err
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if session, ok := r.sessions[id]; ok {
		return session, nil
	}
	r.sessions[id] = candidate
	return candidate, nil
}

func (r *Repository) SaveEntry(sessionID string, entry Entry) error {
	if r.storage == nil {
		return nil
	}
	return r.storage.Append(sessionID, entry)
}

func (r *Repository) Delete(id string) (bool, error) {
	r.mu.Lock()
	_, cached := r.sessions[id]
	delete(r.sessions, id)
	r.mu.Unlock()
	if r.storage == nil {
		return cached, nil
	}
	deleted, err := r.storage.Delete(id)
	return cached || deleted, err
}

func (r *Repository) List() ([]Info, error) {
	r.mu.RLock()
	storage := r.storage
	cached := make(map[string]struct{}, len(r.sessions))
	for id := range r.sessions {
		cached[id] = struct{}{}
	}
	r.mu.RUnlock()

	candidates := make(map[string]*Session)
	if storage != nil {
		ids, err := storage.List()
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if _, ok := cached[id]; ok {
				continue
			}
			session := New(id)
			entries, err := storage.Load(id)
			if err != nil {
				return nil, err
			}
			if err := session.Load(entries); err != nil {
				return nil, err
			}
			candidates[id] = session
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for id, candidate := range candidates {
		if _, exists := r.sessions[id]; !exists {
			r.sessions[id] = candidate
		}
	}
	infos := make([]Info, 0, len(r.sessions))
	for _, session := range r.sessions {
		infos = append(infos, session.Info())
	}
	return infos, nil
}

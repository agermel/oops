package runtime

import (
	"errors"
	"sync"

	"oops/internal/logutil"

	"go.uber.org/zap"
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
	_, err := r.AppendEntry(sessionID, entry)
	return err
}

func (r *Repository) AppendEntry(sessionID string, entry Entry) (Entry, error) {
	session, err := r.Load(sessionID)
	if err != nil {
		return Entry{}, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	candidate := session.cloneLocked()
	if err := candidate.prepareEntryLocked(&entry); err != nil {
		return Entry{}, err
	}
	candidate.storeEntryLocked(entry)
	if err := candidate.validateLoadedHistoryLocked(); err != nil {
		return Entry{}, err
	}
	if r.storage == nil {
		session.publishLocked(candidate)
		return cloneEntry(entry), nil
	}
	if err := r.storage.Append(sessionID, entry); err != nil {
		var commitErr *StorageCommitError
		if !errors.As(err, &commitErr) || !commitErr.Committed {
			return Entry{}, err
		}
		logStorageCommitUncertain(sessionID, commitErr)
	}
	session.publishLocked(candidate)
	return cloneEntry(entry), nil
}

func (r *Repository) AppendBranchSummary(sessionID, leafID, summary string, details any) (Entry, error) {
	session, err := r.Load(sessionID)
	if err != nil {
		return Entry{}, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	candidate := session.cloneLocked()
	if leafID != "" {
		if _, ok := candidate.entries[leafID]; !ok {
			return Entry{}, errors.New("target entry is not in session")
		}
	}
	rawDetails, err := marshalDetails(details)
	if err != nil {
		return Entry{}, err
	}
	candidate.leafID = leafID
	entry := Entry{Type: EntryBranchSummary, Summary: summary, Details: rawDetails}
	if err := candidate.prepareEntryLocked(&entry); err != nil {
		return Entry{}, err
	}
	candidate.storeEntryLocked(entry)
	if err := candidate.validateLoadedHistoryLocked(); err != nil {
		return Entry{}, err
	}
	if r.storage != nil {
		if err := r.storage.Append(sessionID, entry); err != nil {
			var commitErr *StorageCommitError
			if !errors.As(err, &commitErr) || !commitErr.Committed {
				return Entry{}, err
			}
			logStorageCommitUncertain(sessionID, commitErr)
		}
	}
	session.publishLocked(candidate)
	return cloneEntry(entry), nil
}

func (r *Repository) Delete(id string) (bool, error) {
	if r.storage == nil {
		r.mu.Lock()
		_, cached := r.sessions[id]
		delete(r.sessions, id)
		r.mu.Unlock()
		return cached, nil
	}
	deleted, err := r.storage.Delete(id)
	if err != nil {
		var commitErr *StorageCommitError
		if !errors.As(err, &commitErr) || !commitErr.Committed {
			return false, err
		}
		logStorageCommitUncertain(id, commitErr)
		err = nil
	}
	r.mu.Lock()
	_, cached := r.sessions[id]
	if cached || deleted {
		delete(r.sessions, id)
	}
	r.mu.Unlock()
	return cached || deleted, nil
}

func logStorageCommitUncertain(sessionID string, err *StorageCommitError) {
	logutil.Warn("session: storage commit durability uncertain",
		zap.String("session_id", sessionID),
		zap.String("operation", err.Operation),
		zap.String("phase", err.Phase),
		zap.String("error", err.Err.Error()),
	)
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
				var corruptErr *corruptSessionFileError
				if !errors.As(err, &corruptErr) {
					return nil, err
				}
				logutil.Warn("session: skip unreadable file",
					zap.String("session_id", id),
					zap.String("file", corruptErr.file),
					zap.Int("line", corruptErr.line),
					zap.String("reason", string(corruptErr.reason)),
				)
				continue
			}
			if err := session.Load(entries); err != nil {
				logutil.Warn("session: skip unreadable file",
					zap.String("session_id", id),
					zap.String("file", id+".jsonl"),
					zap.String("reason", string(corruptSessionInvalidHistory)),
				)
				continue
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

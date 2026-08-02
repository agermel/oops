package session

import (
	"errors"
	"fmt"
	"io/fs"
	"sync"

	"oops/internal/logutil"

	"go.uber.org/zap"
)

type Repository struct {
	mu       sync.RWMutex
	storage  Storage
	sessions map[string]*Session
	creating map[string]struct{}
	deleting map[string]*Session
}

var (
	ErrSessionExists   = errors.New("session already exists")
	ErrSessionCreating = errors.New("session creation in progress")
	ErrSessionDeleting = errors.New("session deletion in progress")
	ErrSessionMismatch = errors.New("session instance mismatch")
)

func NewRepository(storage Storage) *Repository {
	return &Repository{
		storage:  storage,
		sessions: make(map[string]*Session),
		creating: make(map[string]struct{}),
		deleting: make(map[string]*Session),
	}
}

func (r *Repository) Create(id string) (*Session, error) {
	sess := New(id)
	if err := r.CreateSession(sess); err != nil {
		return nil, err
	}
	return sess, nil
}

func (r *Repository) CreateSession(sess *Session) error {
	if sess == nil {
		return errors.New("session is required")
	}
	sessionID := sess.ID()
	r.mu.Lock()
	if _, deleting := r.deleting[sessionID]; deleting {
		r.mu.Unlock()
		return fmt.Errorf("%w: %q", ErrSessionDeleting, sessionID)
	}
	if existing, exists := r.sessions[sessionID]; exists {
		r.mu.Unlock()
		if existing == sess {
			return nil
		}
		return fmt.Errorf("%w: %q", ErrSessionExists, sessionID)
	}
	if _, exists := r.creating[sessionID]; exists {
		r.mu.Unlock()
		return fmt.Errorf("%w: %q", ErrSessionExists, sessionID)
	}
	if r.creating == nil {
		r.creating = make(map[string]struct{})
	}
	r.creating[sessionID] = struct{}{}
	storage := r.storage
	r.mu.Unlock()

	var createErr error
	if storage != nil {
		createErr = storage.Create(sessionID, sess.Entries())
		if createErr != nil {
			var commitErr *StorageCommitError
			switch {
			case errors.As(createErr, &commitErr) && commitErr.Committed:
				logStorageCommitUncertain(sessionID, commitErr)
				createErr = nil
			case errors.Is(createErr, fs.ErrExist):
				createErr = fmt.Errorf("%w: %q", ErrSessionExists, sessionID)
			}
		}
	}

	r.mu.Lock()
	delete(r.creating, sessionID)
	if createErr == nil {
		if r.sessions == nil {
			r.sessions = make(map[string]*Session)
		}
		r.sessions[sessionID] = sess
	}
	r.mu.Unlock()
	return createErr
}

func (r *Repository) Get(id string) (*Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	session, ok := r.sessions[id]
	return session, ok
}

func (r *Repository) Fork(sourceID string, options ForkOptions) (*Session, error) {
	source, err := r.Load(sourceID)
	if err != nil {
		return nil, err
	}
	fork, err := source.Fork(options)
	if err != nil {
		return nil, err
	}
	forkID := fork.ID()
	if forkID == sourceID {
		return nil, errors.New("fork session id must differ from source")
	}

	if err := r.CreateSession(fork); err != nil {
		return nil, err
	}
	return fork, nil
}

func (r *Repository) Load(id string) (*Session, error) {
	r.mu.RLock()
	if _, deleting := r.deleting[id]; deleting {
		r.mu.RUnlock()
		return nil, fmt.Errorf("%w: %q", ErrSessionDeleting, id)
	}
	if session, ok := r.sessions[id]; ok {
		r.mu.RUnlock()
		return session, nil
	}
	if _, creating := r.creating[id]; creating {
		r.mu.RUnlock()
		return nil, fmt.Errorf("%w: %q", ErrSessionCreating, id)
	}
	storage := r.storage
	r.mu.RUnlock()

	if storage == nil {
		return nil, fmt.Errorf("%w: session %q", fs.ErrNotExist, id)
	}
	candidate := New(id)
	entries, err := storage.Load(id)
	if err != nil {
		return nil, err
	}
	if entries == nil {
		return nil, fmt.Errorf("%w: session %q", fs.ErrNotExist, id)
	}
	if err := candidate.Load(entries); err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if session, ok := r.sessions[id]; ok {
		return session, nil
	}
	if _, creating := r.creating[id]; creating {
		return nil, fmt.Errorf("%w: %q", ErrSessionCreating, id)
	}
	if _, deleting := r.deleting[id]; deleting {
		return nil, fmt.Errorf("%w: %q", ErrSessionDeleting, id)
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

func (r *Repository) AppendCompaction(sessionID, summary, firstKeptEntryID string, tokensBefore int, details any) (Entry, error) {
	rawDetails, err := marshalDetails(details)
	if err != nil {
		return Entry{}, err
	}
	return r.AppendEntry(sessionID, Entry{
		Type:             EntryCompaction,
		Summary:          summary,
		FirstKeptEntryID: firstKeptEntryID,
		TokensBefore:     tokensBefore,
		Details:          rawDetails,
	})
}

func (r *Repository) AppendLabel(sessionID, targetID, label string) (Entry, error) {
	return r.AppendEntry(sessionID, Entry{Type: EntryLabel, TargetID: targetID, Label: label})
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
	return r.delete(id, nil, false)
}

func (r *Repository) DeleteSession(expected *Session) (bool, error) {
	if expected == nil {
		return false, errors.New("session is required")
	}
	return r.delete(expected.ID(), expected, true)
}

func (r *Repository) delete(id string, expected *Session, requireMatch bool) (bool, error) {
	r.mu.Lock()
	if _, creating := r.creating[id]; creating {
		r.mu.Unlock()
		return false, fmt.Errorf("%w: %q", ErrSessionCreating, id)
	}
	if _, deleting := r.deleting[id]; deleting {
		r.mu.Unlock()
		return false, fmt.Errorf("%w: %q", ErrSessionDeleting, id)
	}
	cachedSession, cached := r.sessions[id]
	if requireMatch && (!cached || cachedSession != expected) {
		r.mu.Unlock()
		return false, fmt.Errorf("%w: %q", ErrSessionMismatch, id)
	}
	if r.deleting == nil {
		r.deleting = make(map[string]*Session)
	}
	r.deleting[id] = cachedSession
	delete(r.sessions, id)
	storage := r.storage
	r.mu.Unlock()

	deleted := false
	var err error
	if storage != nil {
		deleted, err = storage.Delete(id)
	}
	if err != nil {
		var commitErr *StorageCommitError
		if !errors.As(err, &commitErr) || !commitErr.Committed {
			r.mu.Lock()
			delete(r.deleting, id)
			if cached && r.sessions[id] == nil {
				r.sessions[id] = cachedSession
			}
			r.mu.Unlock()
			return false, err
		}
		logStorageCommitUncertain(id, commitErr)
	}
	r.mu.Lock()
	delete(r.deleting, id)
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
	creating := make(map[string]struct{}, len(r.creating))
	for id := range r.creating {
		creating[id] = struct{}{}
	}
	deleting := make(map[string]struct{}, len(r.deleting))
	for id := range r.deleting {
		deleting[id] = struct{}{}
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
			if _, ok := creating[id]; ok {
				continue
			}
			if _, ok := deleting[id]; ok {
				continue
			}
			session := New(id)
			entries, err := storage.Load(id)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
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
			if entries == nil {
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
	for id, candidate := range candidates {
		if _, exists := r.sessions[id]; exists {
			continue
		}
		if _, creating := r.creating[id]; !creating {
			if _, deleting := r.deleting[id]; !deleting {
				r.sessions[id] = candidate
			}
		}
	}
	sessions := make([]*Session, 0, len(r.sessions))
	for _, session := range r.sessions {
		sessions = append(sessions, session)
	}
	r.mu.Unlock()

	infos := make([]Info, 0, len(sessions))
	for _, session := range sessions {
		infos = append(infos, session.Info())
	}
	return infos, nil
}

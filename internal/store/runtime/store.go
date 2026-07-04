package runtime

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

const (
	EnvPath     = "OOPS_RUNTIME_DB"
	DefaultPath = "data/runtime.db"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

var gooseMu sync.Mutex

type Store struct {
	db *sql.DB
	q  *Queries
}

type NodeletRecord struct {
	ID      string
	Name    string
	Address string
	Token   string
}

type MCPConnectionRecord struct {
	ID          string
	Name        string
	Type        string
	Transport   string
	Command     string
	Args        []string
	Env         []string
	URL         string
	Enabled     bool
	ContainerID string
	NodeletID   string
}

type ProjectRecord struct {
	ID                    string
	Name                  string
	Description           string
	GitHubRepo            string
	NodeletIDs            []string
	ExcludedContainerRefs []string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type DSNRecord struct {
	NodeletID   string
	ContainerID string
	Pairs       map[string]string
}

type UserRecord struct {
	Username string
	Name     string
	Password string // bcrypt hash
}

func OpenRuntime() (*Store, error) {
	path := os.Getenv(EnvPath)
	if path == "" {
		path = DefaultPath
	}
	return Open(path)
}

func Open(path string) (*Store, error) {
	if path == "" {
		path = DefaultPath
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create runtime db dir: %w", err)
	}

	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db, q: New(db)}, nil
}

func migrate(db *sql.DB) error {
	gooseMu.Lock()
	defer gooseMu.Unlock()

	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("migrate runtime db: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) CountNodelets(ctx context.Context) (int64, error) {
	return s.q.CountNodelets(ctx)
}

func (s *Store) ListNodelets(ctx context.Context) ([]NodeletRecord, error) {
	rows, err := s.q.ListNodelets(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]NodeletRecord, len(rows))
	for i, row := range rows {
		out[i] = NodeletRecord{
			ID:      row.ID,
			Name:    row.Name,
			Address: row.Address,
			Token:   row.Token,
		}
	}
	return out, nil
}

func (s *Store) ReplaceNodelets(ctx context.Context, rows []NodeletRecord) error {
	return s.tx(ctx, func(q *Queries) error {
		if err := q.DeleteAllNodelets(ctx); err != nil {
			return err
		}
		for _, row := range rows {
			if err := q.InsertNodelet(ctx, InsertNodeletParams{
				ID:      row.ID,
				Name:    row.Name,
				Address: row.Address,
				Token:   row.Token,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) CountMCPConnections(ctx context.Context) (int64, error) {
	return s.q.CountMCPConnections(ctx)
}

func (s *Store) ListMCPConnections(ctx context.Context) ([]MCPConnectionRecord, error) {
	rows, err := s.q.ListMCPConnections(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]MCPConnectionRecord, 0, len(rows))
	for _, row := range rows {
		var args []string
		var env []string
		if err := json.Unmarshal([]byte(row.ArgsJson), &args); err != nil {
			return nil, fmt.Errorf("parse args for mcp connection %q: %w", row.ID, err)
		}
		if err := json.Unmarshal([]byte(row.EnvJson), &env); err != nil {
			return nil, fmt.Errorf("parse env for mcp connection %q: %w", row.ID, err)
		}
		out = append(out, MCPConnectionRecord{
			ID:          row.ID,
			Name:        row.Name,
			Type:        row.Type,
			Transport:   row.Transport,
			Command:     row.Command,
			Args:        args,
			Env:         env,
			URL:         row.Url,
			Enabled:     row.Enabled != 0,
			ContainerID: row.ContainerID,
			NodeletID:   row.NodeletID,
		})
	}
	return out, nil
}

func (s *Store) ReplaceMCPConnections(ctx context.Context, rows []MCPConnectionRecord) error {
	return s.tx(ctx, func(q *Queries) error {
		if err := q.DeleteAllMCPConnections(ctx); err != nil {
			return err
		}
		for _, row := range rows {
			args, err := json.Marshal(row.Args)
			if err != nil {
				return fmt.Errorf("marshal args for mcp connection %q: %w", row.ID, err)
			}
			env, err := json.Marshal(row.Env)
			if err != nil {
				return fmt.Errorf("marshal env for mcp connection %q: %w", row.ID, err)
			}
			enabled := int64(0)
			if row.Enabled {
				enabled = 1
			}
			if err := q.InsertMCPConnection(ctx, InsertMCPConnectionParams{
				ID:          row.ID,
				Name:        row.Name,
				Type:        row.Type,
				Transport:   row.Transport,
				Command:     row.Command,
				ArgsJson:    string(args),
				EnvJson:     string(env),
				Url:         row.URL,
				Enabled:     enabled,
				ContainerID: row.ContainerID,
				NodeletID:   row.NodeletID,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) CountProjects(ctx context.Context) (int64, error) {
	return s.q.CountProjects(ctx)
}

func (s *Store) ListProjects(ctx context.Context) ([]ProjectRecord, error) {
	projects, err := s.q.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	nodelets, err := s.q.ListProjectNodelets(ctx)
	if err != nil {
		return nil, err
	}
	exclusions, err := s.q.ListProjectExclusions(ctx)
	if err != nil {
		return nil, err
	}

	byID := make(map[string]int, len(projects))
	out := make([]ProjectRecord, len(projects))
	for i, row := range projects {
		out[i] = ProjectRecord{
			ID:          row.ID,
			Name:        row.Name,
			Description: row.Description,
			GitHubRepo:  row.GithubRepo,
			NodeletIDs:  []string{},
			CreatedAt:   timeFromUnixMilli(row.CreatedAt),
			UpdatedAt:   timeFromUnixMilli(row.UpdatedAt),
		}
		byID[row.ID] = i
	}
	for _, row := range nodelets {
		if i, ok := byID[row.ProjectID]; ok {
			out[i].NodeletIDs = append(out[i].NodeletIDs, row.NodeletID)
		}
	}
	for _, row := range exclusions {
		if i, ok := byID[row.ProjectID]; ok {
			out[i].ExcludedContainerRefs = append(out[i].ExcludedContainerRefs, row.Ref)
		}
	}
	return out, nil
}

func (s *Store) ReplaceProjects(ctx context.Context, rows []ProjectRecord) error {
	return s.tx(ctx, func(q *Queries) error {
		if err := q.DeleteAllProjectExclusions(ctx); err != nil {
			return err
		}
		if err := q.DeleteAllProjectNodelets(ctx); err != nil {
			return err
		}
		if err := q.DeleteAllProjects(ctx); err != nil {
			return err
		}
		for _, row := range rows {
			if err := q.InsertProject(ctx, InsertProjectParams{
				ID:          row.ID,
				Name:        row.Name,
				Description: row.Description,
				GithubRepo:  row.GitHubRepo,
				CreatedAt:   timeToUnixMilli(row.CreatedAt),
				UpdatedAt:   timeToUnixMilli(row.UpdatedAt),
			}); err != nil {
				return err
			}
			for i, nodeletID := range row.NodeletIDs {
				if err := q.InsertProjectNodelet(ctx, InsertProjectNodeletParams{
					ProjectID: row.ID,
					NodeletID: nodeletID,
					Position:  int64(i),
				}); err != nil {
					return err
				}
			}
			for _, ref := range row.ExcludedContainerRefs {
				if err := q.InsertProjectExclusion(ctx, InsertProjectExclusionParams{
					ProjectID: row.ID,
					Ref:       ref,
				}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func timeToUnixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func timeFromUnixMilli(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func (s *Store) CountDSNEntries(ctx context.Context) (int64, error) {
	return s.q.CountDSNEntries(ctx)
}

func (s *Store) ListDSNRecords(ctx context.Context) ([]DSNRecord, error) {
	rows, err := s.q.ListDSNEntries(ctx)
	if err != nil {
		return nil, err
	}
	index := make(map[string]int)
	out := []DSNRecord{}
	for _, row := range rows {
		key := row.NodeletID + "\x00" + row.ContainerID
		i, ok := index[key]
		if !ok {
			i = len(out)
			index[key] = i
			out = append(out, DSNRecord{
				NodeletID:   row.NodeletID,
				ContainerID: row.ContainerID,
				Pairs:       make(map[string]string),
			})
		}
		out[i].Pairs[row.Key] = row.Value
	}
	return out, nil
}

func (s *Store) ReplaceDSNRecords(ctx context.Context, rows []DSNRecord) error {
	return s.tx(ctx, func(q *Queries) error {
		if err := q.DeleteAllDSNEntries(ctx); err != nil {
			return err
		}
		for _, row := range rows {
			for k, v := range row.Pairs {
				if err := q.InsertDSNEntry(ctx, InsertDSNEntryParams{
					NodeletID:   row.NodeletID,
					ContainerID: row.ContainerID,
					Key:         k,
					Value:       v,
				}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *Store) GetUser(ctx context.Context) (*UserRecord, error) {
	u, err := s.q.GetUser(ctx)
	if err != nil {
		return nil, err
	}
	return &UserRecord{
		Username: u.Username,
		Name:     u.Name,
		Password: u.Password,
	}, nil
}

func (s *Store) UpsertUser(ctx context.Context, u UserRecord) error {
	return s.q.UpsertUser(ctx, UpsertUserParams{
		Username: u.Username,
		Name:     u.Name,
		Password: u.Password,
	})
}

func (s *Store) tx(ctx context.Context, fn func(*Queries) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(s.q.WithTx(tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

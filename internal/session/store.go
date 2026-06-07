package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/raydraw/ergate/internal/llm"
)

// TurnMetrics records per-turn timing and token data.
type TurnMetrics struct {
	Turn            int           `json:"turn"`
	Model           string        `json:"model"`
	LatencyMS       int64         `json:"latency_ms"`
	TTFTMS          int64         `json:"ttft_ms"`
	TokensIn        int           `json:"tokens_in"`
	TokensOut       int           `json:"tokens_out"`
	CacheHitTokens  int           `json:"cache_hit_tokens"`
	CacheMissTokens int           `json:"cache_miss_tokens"`
	ToolsRan        int           `json:"tools_ran"`
	Compacted       bool          `json:"compacted"`
	StartedAt       time.Time     `json:"started_at"`
}

// Session holds a saved conversation.
type Session struct {
	ID        string        `json:"id"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	Model     string        `json:"model"`
	Messages  []llm.Message `json:"messages"`
	Usage     llm.Usage     `json:"usage"`
	Turns     []TurnMetrics `json:"turns,omitempty"`
}

// AddTurn appends a turn metric to the session.
func (s *Session) AddTurn(m TurnMetrics) {
	s.Turns = append(s.Turns, m)
}

// LastTurn returns the most recent turn metrics, or nil.
func (s *Session) LastTurn() *TurnMetrics {
	if len(s.Turns) == 0 {
		return nil
	}
	return &s.Turns[len(s.Turns)-1]
}

// Store persists sessions as JSON files.
type Store struct {
	dir string
}

// NewStore creates a session store.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}
	return &Store{dir: dir}, nil
}

// Save writes a session to disk.
func (s *Store) Save(sess *Session) error {
	sess.UpdatedAt = time.Now()
	if sess.ID == "" {
		sess.ID = fmt.Sprintf("session_%d", sess.CreatedAt.Unix())
	}
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now()
	}

	path := filepath.Join(s.dir, sess.ID+".json")
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}

// Load reads a session by ID.
func (s *Store) Load(id string) (*Session, error) {
	// Support loading by ID or by filename
	path := id
	if !strings.HasSuffix(id, ".json") {
		path = filepath.Join(s.dir, id+".json")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.dir, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}

	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}
	return &sess, nil
}

// List returns all saved session IDs, sorted by most recent first.
func (s *Store) List() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	type sessionInfo struct {
		id    string
		mtime time.Time
	}

	var infos []sessionInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		infos = append(infos, sessionInfo{id: id, mtime: info.ModTime()})
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].mtime.After(infos[j].mtime)
	})

	ids := make([]string, len(infos))
	for i, info := range infos {
		ids[i] = info.id
	}
	return ids, nil
}

// Latest returns the most recently modified session, or nil.
func (s *Store) Latest() (*Session, error) {
	ids, err := s.List()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return s.Load(ids[0])
}

// Delete removes a session.
func (s *Store) Delete(id string) error {
	path := filepath.Join(s.dir, id+".json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Prune keeps the most recent N sessions and deletes the rest.
func (s *Store) Prune(keep int) {
	ids, err := s.List()
	if err != nil || len(ids) <= keep {
		return
	}
	for _, id := range ids[keep:] {
		_ = s.Delete(id)
	}
}

package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/raydraw/ergate/internal/llm"
)

// --- Service interface ---

// Service is the abstraction over session persistence.
// Implementations: FileService (JSON files), future SQLite, etc.
type Service interface {
	// Create creates a new session. Returns error if session already exists.
	Create(ctx context.Context, req *CreateRequest) (*CreateResponse, error)

	// Get retrieves a session. NumRecentEvents limits loaded events (0 = all).
	Get(ctx context.Context, req *GetRequest) (*GetResponse, error)

	// List returns all sessions for the given user.
	List(ctx context.Context, req *ListRequest) (*ListResponse, error)

	// Delete removes a session permanently.
	Delete(ctx context.Context, req *DeleteRequest) error

	// AppendEvent appends a single event to the session.
	// Partial events (streaming) are not persisted.
	AppendEvent(ctx context.Context, sess *Session, event *Event) error

	// Save persists a session fully (upsert). For incremental writes, prefer AppendEvent.
	Save(ctx context.Context, sess *Session) error

	// Prune keeps the most recent N sessions and deletes the rest.
	Prune(ctx context.Context, keep int)
}

// --- Request / Response types ---

// CreateRequest is the input for Service.Create.
type CreateRequest struct {
	AppName   string
	UserID    string
	SessionID string         // empty = auto-generate
	State     map[string]any // optional initial state
}

// CreateResponse is the output of Service.Create.
type CreateResponse struct {
	Session *Session
}

// GetRequest is the input for Service.Get.
type GetRequest struct {
	AppName         string
	UserID          string
	SessionID       string
	NumRecentEvents int // 0 = load all events
}

// GetResponse is the output of Service.Get.
type GetResponse struct {
	Session *Session
}

// ListRequest is the input for Service.List.
type ListRequest struct {
	AppName string
	UserID  string
}

// ListResponse is the output of Service.List.
type ListResponse struct {
	Sessions []*Session
}

// DeleteRequest is the input for Service.Delete.
type DeleteRequest struct {
	AppName   string
	UserID    string
	SessionID string
}

// --- Event type ---

// Event represents a single interaction in a conversation.
// It wraps an llm.Message with persistence metadata.
type Event struct {
	ID        string      `json:"id"`
	Timestamp time.Time   `json:"timestamp"`
	Author    string      `json:"author"` // "user", "assistant", or tool name
	Message   llm.Message `json:"message"`
	Partial   bool        `json:"-"` // streaming partial events are not persisted
}

// TurnMetrics records per-turn timing and token data.
type TurnMetrics struct {
	Turn            int       `json:"turn"`
	Model           string    `json:"model"`
	LatencyMS       int64     `json:"latency_ms"`
	TTFTMS          int64     `json:"ttft_ms"`
	TokensIn        int       `json:"tokens_in"`
	TokensOut       int       `json:"tokens_out"`
	CacheHitTokens  int       `json:"cache_hit_tokens"`
	CacheMissTokens int       `json:"cache_miss_tokens"`
	ToolsRan        int       `json:"tools_ran"`
	Compacted       bool      `json:"compacted"`
	StartedAt       time.Time `json:"started_at"`
}

// Session holds a saved conversation.
type Session struct {
	ID        string        `json:"id"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	Model     string        `json:"model"`
	Messages  []llm.Message `json:"messages"`
	Events    []*Event      `json:"events,omitempty"`
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

// --- FileService implementation ---

// FileService persists sessions as JSON files on disk.
type FileService struct {
	Dir string
}

// NewFileService creates a FileService.
func NewFileService(dir string) (*FileService, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}
	return &FileService{Dir: dir}, nil
}

var _ Service = (*FileService)(nil)

func (s *FileService) filePath(sessionID string) string {
	return filepath.Join(s.Dir, sessionID+".json")
}

// Create creates a new session.
func (s *FileService) Create(ctx context.Context, req *CreateRequest) (*CreateResponse, error) {
	_ = ctx // reserved for future use (tracing, cancellation)
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("session_%d", time.Now().UnixNano())
	}

	path := s.filePath(sessionID)
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("session %s already exists", sessionID)
	}

	sess := &Session{
		ID:        sessionID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Events:    make([]*Event, 0),
	}

	if err := s.writeSession(sess); err != nil {
		return nil, err
	}

	return &CreateResponse{Session: sess}, nil
}

// Get retrieves a session.
func (s *FileService) Get(ctx context.Context, req *GetRequest) (*GetResponse, error) {
	_ = ctx
	path := req.SessionID
	if !strings.HasSuffix(path, ".json") {
		path = s.filePath(req.SessionID)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.Dir, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}

	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}

	// Apply NumRecentEvents filter.
	if req.NumRecentEvents > 0 && len(sess.Events) > req.NumRecentEvents {
		sess.Events = sess.Events[len(sess.Events)-req.NumRecentEvents:]
	}

	return &GetResponse{Session: &sess}, nil
}

// List returns all sessions for the given app/user.
func (s *FileService) List(ctx context.Context, req *ListRequest) (*ListResponse, error) {
	_ = ctx
	_ = req // AppName/UserID unused in file-based storage; reserved for future

	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return &ListResponse{}, nil
		}
		return nil, err
	}

	type sessionMeta struct {
		sess  *Session
		mtime time.Time
	}

	var metas []sessionMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		// Load session metadata (skip messages for perf).
		sess, err := s.loadMeta(id)
		if err != nil {
			continue
		}
		metas = append(metas, sessionMeta{sess: sess, mtime: info.ModTime()})
	}

	sort.Slice(metas, func(i, j int) bool {
		return metas[i].mtime.After(metas[j].mtime)
	})

	sessions := make([]*Session, len(metas))
	for i, m := range metas {
		sessions[i] = m.sess
	}

	return &ListResponse{Sessions: sessions}, nil
}

// Delete removes a session.
func (s *FileService) Delete(ctx context.Context, req *DeleteRequest) error {
	_ = ctx
	path := s.filePath(req.SessionID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// AppendEvent appends an event to the session and persists.
func (s *FileService) AppendEvent(ctx context.Context, sess *Session, event *Event) error {
	_ = ctx
	if event == nil || event.Partial {
		return nil
	}

	// Ensure the in-memory session is up to date.
	sess.Events = append(sess.Events, event)
	sess.Messages = append(sess.Messages, event.Message)
	sess.UpdatedAt = time.Now()

	return s.writeSession(sess)
}

// --- Internal helpers ---

func (s *FileService) writeSession(sess *Session) error {
	sess.UpdatedAt = time.Now()
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now()
	}

	path := s.filePath(sess.ID)
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}

// loadMeta loads session metadata only (for List).
func (s *FileService) loadMeta(id string) (*Session, error) {
	path := s.filePath(id)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

// Prune keeps the most recent N sessions and deletes the rest.
func (s *FileService) Prune(ctx context.Context, keep int) {
	_ = ctx
	resp, err := s.List(context.Background(), &ListRequest{})
	if err != nil || len(resp.Sessions) <= keep {
		return
	}
	for _, sess := range resp.Sessions[keep:] {
		_ = s.Delete(context.Background(), &DeleteRequest{SessionID: sess.ID})
	}
}

// Save persists a session fully (upsert). For incremental writes, prefer AppendEvent.
func (s *FileService) Save(ctx context.Context, sess *Session) error {
	_ = ctx
	return s.writeSession(sess)
}

// --- Backward-compatible convenience methods (for migration) ---

// Load reads a session by ID (deprecated: use Get).
// Kept for backward compatibility during migration.
func (s *FileService) Load(id string) (*Session, error) {
	resp, err := s.Get(context.Background(), &GetRequest{SessionID: id})
	if err != nil {
		return nil, err
	}
	return resp.Session, nil
}

// Latest returns the most recently modified session, or nil.
func (s *FileService) Latest() (*Session, error) {
	resp, err := s.List(context.Background(), &ListRequest{})
	if err != nil {
		return nil, err
	}
	if len(resp.Sessions) == 0 {
		return nil, nil
	}
	return resp.Sessions[0], nil
}

// --- Legacy types for backward compatibility ---

// Store is the legacy name for FileService.
// Deprecated: use Service interface and NewFileService.
type Store = FileService

// NewStore creates a session store.
// Deprecated: use NewFileService.
func NewStore(dir string) (*Store, error) {
	return NewFileService(dir)
}

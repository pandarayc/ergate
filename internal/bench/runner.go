package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/raydraw/ergate/internal/config"
	"github.com/raydraw/ergate/internal/engine"
	"github.com/raydraw/ergate/internal/filehistory"
	"github.com/raydraw/ergate/internal/hooks"
	"github.com/raydraw/ergate/internal/llm"
	"github.com/raydraw/ergate/internal/planmode"
	"github.com/raydraw/ergate/internal/session"
	"github.com/raydraw/ergate/internal/skill"
	"github.com/raydraw/ergate/internal/tool"
	"github.com/raydraw/ergate/internal/trace"
)

type Task struct {
	ID          string        `json:"id"`
	Instruction string        `json:"instruction"`
	WorkDir     string        `json:"work_dir,omitempty"`
	TestCmd     string        `json:"test_cmd"`
	Timeout     time.Duration `json:"timeout,omitempty"`
	MaxTurns    int           `json:"max_turns,omitempty"`
}

func LoadTask(taskDir string) (*Task, error) {
	id := filepath.Base(taskDir)
	instBytes, err := os.ReadFile(filepath.Join(taskDir, "instruction.txt"))
	if err != nil {
		return nil, fmt.Errorf("read instruction.txt: %w", err)
	}
	testPath := filepath.Join(taskDir, "test.sh")
	if _, err := os.Stat(testPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("test.sh not found in %s", taskDir)
	}
	return &Task{
		ID: id, Instruction: string(instBytes), WorkDir: taskDir,
		TestCmd: "bash " + testPath, Timeout: 10 * time.Minute, MaxTurns: 25,
	}, nil
}

type Result struct {
	TaskID     string           `json:"task_id"`
	Pass       bool             `json:"pass"`
	TestOutput string           `json:"test_output,omitempty"`
	TestExit   int              `json:"test_exit_code"`
	Trace      *trace.TaskTrace `json:"trace"`
	Error      string           `json:"error,omitempty"`
	Duration   time.Duration    `json:"duration"`
	SessionID  string           `json:"session_id,omitempty"`
}

type Runner struct {
	cfg      *config.Config
	client   llm.LLMClient
	registry *tool.Registry
	benchDir string
}

func NewRunner(cfg *config.Config, client llm.LLMClient, registry *tool.Registry, benchDir string) *Runner {
	return &Runner{cfg: cfg, client: client, registry: registry, benchDir: benchDir}
}

func (r *Runner) Run(ctx context.Context, task *Task) *Result {
	start := time.Now()
	res := &Result{TaskID: task.ID}

	workDir := task.WorkDir
	if workDir == "" {
		workDir = filepath.Join(r.benchDir, task.ID)
	}
	if task.MaxTurns > 0 {
		r.cfg.MaxTurns = task.MaxTurns
	}
	timeout := task.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resultsDir := filepath.Join(r.benchDir, "results", task.ID)
	os.MkdirAll(resultsDir, 0o700)
	sessionDir := filepath.Join(resultsDir, "sessions")
	transcriptDir := filepath.Join(resultsDir, "transcripts")

	eng := r.createEngine(workDir, sessionDir, transcriptDir)

	events := make(chan engine.Event, 256)
	runErr := make(chan error, 1)
	go func() { runErr <- eng.Run(ctx, task.Instruction, events) }()
	for range events {}
	if err := <-runErr; err != nil {
		res.Error = fmt.Sprintf("engine: %v", err)
	}

	res.SessionID = eng.SessionID()

	// Load session from disk (saved by engine's deferred SaveSession).
	sess := loadLatestSessionFile(sessionDir)
	if sess == nil {
		data := eng.ExportSession()
		sess = &session.Session{
			ID: task.ID, Model: r.cfg.Model,
			Messages: data.Messages, Usage: data.Usage, Turns: data.Turns,
		}
	}
	res.Trace = trace.ExtractTaskTrace(sess, task.ID, task.Instruction)

	if task.TestCmd != "" {
		res.Pass, res.TestOutput, res.TestExit = r.runTest(task.TestCmd, workDir)
	}
	res.Duration = time.Since(start)
	return res
}

func (r *Runner) SaveResult(taskID string, result *Result) error {
	dir := filepath.Join(r.benchDir, "results", taskID)
	os.MkdirAll(dir, 0o700)
	data, _ := json.MarshalIndent(result, "", "  ")
	return os.WriteFile(filepath.Join(dir, "result.json"), data, 0o644)
}

func (r *Runner) createEngine(workDir, sessionDir, transcriptDir string) *engine.Engine {
	cwd, _ := os.Getwd()
	sessionSvc, _ := session.NewFileService(sessionDir)
	ectx := engine.Context{
		Skills: skill.NewRegistry(), Hooks: benchmarkHooks(),
		FileTracker: filehistory.NewTracker(cwd), PlanMgr: planmode.NewManager(),
		TranscriptDir: transcriptDir, SessionService: sessionSvc,
		PermMgr: tool.NewPermissionManager("bypass", nil),
		PermCtx: tool.PermissionContext{
			Mode:             tool.PermModeBypassPermissions,
			AlwaysAllowRules: make(map[string][]tool.PermissionRule),
			AlwaysDenyRules:  make(map[string][]tool.PermissionRule),
			AlwaysAskRules:   make(map[string][]tool.PermissionRule),
		},
	}
	_ = workDir
	return engine.New(r.cfg, r.client, r.registry, ectx)
}

func (r *Runner) runTest(testCmd, workDir string) (bool, string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", testCmd)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	output := string(out)
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
			output += "\n[test error: " + err.Error() + "]"
		}
	}
	return exitCode == 0, output, exitCode
}

// loadLatestSessionFile finds the most recent session JSON file in dir and loads it.
func loadLatestSessionFile(dir string) *session.Session {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "session_") && strings.HasSuffix(e.Name(), ".json") {
			files = append(files, e)
		}
	}
	if len(files) == 0 {
		return nil
	}
	sort.Slice(files, func(i, j int) bool {
		infoI, _ := files[i].Info()
		infoJ, _ := files[j].Info()
		return infoI.ModTime().After(infoJ.ModTime())
	})
	path := filepath.Join(dir, files[0].Name())
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var sess session.Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil
	}
	return &sess
}

func benchmarkHooks() *hooks.Manager {
	m := hooks.NewManager()
	m.Register(hooks.NewPhaseEnforcer(3))
	return m
}

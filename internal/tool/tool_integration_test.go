package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func setupTools(t *testing.T) (*Registry, *ExecContext, string) {
	t.Helper()
	dir := t.TempDir()

	reg := NewRegistry()
	RegisterBuiltins(reg, NewTodoManager())

	execCtx := &ExecContext{CWD: dir}

	// Create a test file
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("line one\nline two\nline three\n"), 0o644)

	return reg, execCtx, dir
}

func TestReadToolExecute(t *testing.T) {
	reg, exec, dir := setupTools(t)

	input, _ := json.Marshal(map[string]string{"file_path": filepath.Join(dir, "hello.txt")})
	result, err := reg.Execute(context.Background(), "Read", input, exec)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Errorf("read failed: %s", result.Content)
	}
	if result.Content == "" {
		t.Error("expected non-empty content")
	}
}

func TestReadToolWithOffset(t *testing.T) {
	reg, exec, dir := setupTools(t)

	input, _ := json.Marshal(map[string]any{"file_path": filepath.Join(dir, "hello.txt"), "offset": 2, "limit": 1})
	result, err := reg.Execute(context.Background(), "Read", input, exec)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Errorf("read failed: %s", result.Content)
	}
}

func TestWriteToolExecute(t *testing.T) {
	reg, exec, dir := setupTools(t)

	path := filepath.Join(dir, "newfile.txt")
	input, _ := json.Marshal(map[string]string{"file_path": path, "content": "hello world"})
	result, err := reg.Execute(context.Background(), "Write", input, exec)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Errorf("write failed: %s", result.Content)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "hello world" {
		t.Errorf("file content mismatch: got %q", string(data))
	}
}

func TestEditToolExecute(t *testing.T) {
	reg, exec, dir := setupTools(t)

	path := filepath.Join(dir, "hello.txt")
	input, _ := json.Marshal(map[string]string{
		"file_path":  path,
		"old_string": "line two\n",
		"new_string": "replaced\n",
	})
	result, err := reg.Execute(context.Background(), "Edit", input, exec)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Errorf("edit failed: %s", result.Content)
	}

	data, _ := os.ReadFile(path)
	expected := "line one\nreplaced\nline three\n"
	if string(data) != expected {
		t.Errorf("file content mismatch:\ngot  %q\nwant %q", string(data), expected)
	}
}

func TestEditToolReplaceAll(t *testing.T) {
	reg, exec, dir := setupTools(t)

	os.WriteFile(filepath.Join(dir, "dups.txt"), []byte("x x x\n"), 0o644)
	path := filepath.Join(dir, "dups.txt")
	input, _ := json.Marshal(map[string]any{
		"file_path":   path,
		"old_string":  "x",
		"new_string":  "y",
		"replace_all": true,
	})
	result, err := reg.Execute(context.Background(), "Edit", input, exec)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Errorf("edit failed: %s", result.Content)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "y y y\n" {
		t.Errorf("file content mismatch: got %q", string(data))
	}
}

func TestGrepToolExecute(t *testing.T) {
	reg, exec, dir := setupTools(t)

	input, _ := json.Marshal(map[string]string{"pattern": "line", "path": dir})
	result, err := reg.Execute(context.Background(), "Grep", input, exec)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Errorf("grep failed: %s", result.Content)
	}
	if result.Content == "" {
		t.Error("expected non-empty grep output")
	}
}

func TestGrepToolFilesWithMatches(t *testing.T) {
	reg, exec, dir := setupTools(t)

	input, _ := json.Marshal(map[string]string{"pattern": "line", "path": dir, "output_mode": "files_with_matches"})
	result, err := reg.Execute(context.Background(), "Grep", input, exec)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Errorf("grep failed: %s", result.Content)
	}
}

func TestGlobToolExecute(t *testing.T) {
	reg, exec, dir := setupTools(t)

	input, _ := json.Marshal(map[string]string{"pattern": "*.txt", "path": dir})
	result, err := reg.Execute(context.Background(), "Glob", input, exec)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Errorf("glob failed: %s", result.Content)
	}
}

func TestBashToolExecute(t *testing.T) {
	reg, exec, _ := setupTools(t)

	input, _ := json.Marshal(map[string]string{"command": "echo hello bash", "description": "test"})
	result, err := reg.Execute(context.Background(), "Bash", input, exec)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Errorf("bash failed: %s", result.Content)
	}
}

func TestBashToolSafetyBlock(t *testing.T) {
	reg, exec, _ := setupTools(t)

	input, _ := json.Marshal(map[string]string{"command": "sudo rm -rf /tmp", "description": "test"})
	result, err := reg.Execute(context.Background(), "Bash", input, exec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Success {
		t.Error("expected sudo to be blocked")
	}
}

func TestBashToolDangerousPattern(t *testing.T) {
	reg, exec, _ := setupTools(t)

	input, _ := json.Marshal(map[string]string{"command": "rm -rf /", "description": "test"})
	result, err := reg.Execute(context.Background(), "Bash", input, exec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Success {
		t.Error("expected rm -rf / to be blocked")
	}
}

func TestBashToolIsReadOnly(t *testing.T) {
	reg, _, _ := setupTools(t)
	tool, _ := reg.Get("Bash")

	input := json.RawMessage(`{"command": "ls -la"}`)
	if !tool.IsReadOnly(input) {
		t.Error("expected ls to be read-only")
	}

	input = json.RawMessage(`{"command": "rm file.txt"}`)
	if tool.IsReadOnly(input) {
		t.Error("expected rm to NOT be read-only")
	}
}

func TestPermissionManager(t *testing.T) {
	pm := NewPermissionManager("always", nil)
	if err := pm.Check(context.Background(), "Bash", json.RawMessage(`{}`)); err != nil {
		t.Errorf("always mode should allow: %v", err)
	}

	pm = NewPermissionManager("normal", nil)
	// In normal mode without prompt, check returns an error for headless
	err := pm.Check(context.Background(), "Bash", json.RawMessage(`{}`))
	// Normal mode without prompt function returns error in headless
	t.Logf("normal mode check: %v", err)
}

func TestIsShellSafety(t *testing.T) {
	tests := []struct {
		cmd  string
		safe bool
	}{
		{"ls -la", true},
		{"echo hello", true},
		{"grep pattern file", true},
		{"sudo rm -rf /", false},
		{"rm -rf /", false},
		{"shutdown -h now", false},
		{"reboot", false},
		{"mount /dev/sda1 /mnt", false},
	}
	for _, tt := range tests {
		safe, reason := IsShellSafe(tt.cmd)
		if safe != tt.safe {
			t.Errorf("IsShellSafe(%q) = %v (reason: %s), want safe=%v", tt.cmd, safe, reason, tt.safe)
		}
	}
}

func TestEditToolNonUnique(t *testing.T) {
	reg, exec, dir := setupTools(t)

	os.WriteFile(filepath.Join(dir, "dups2.txt"), []byte("dup\nmiddle\ndup\n"), 0o644)
	path := filepath.Join(dir, "dups2.txt")
	input, _ := json.Marshal(map[string]string{
		"file_path":  path,
		"old_string": "dup\n",
		"new_string": "replaced\n",
	})
	result, err := reg.Execute(context.Background(), "Edit", input, exec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Success {
		t.Error("expected failure for non-unique match without replace_all")
	}
}

func TestWriteToolRequiresAbsolutePath(t *testing.T) {
	reg, exec, _ := setupTools(t)

	input, _ := json.Marshal(map[string]string{"file_path": "relative.txt", "content": "test"})
	result, err := reg.Execute(context.Background(), "Write", input, exec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Success {
		t.Error("expected failure for relative path")
	}
}

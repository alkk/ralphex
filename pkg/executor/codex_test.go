package executor

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/executor/mocks"
)

// mockReader creates an io.Reader from string content.
func mockReader(content string) io.Reader {
	return strings.NewReader(content)
}

// mockWait returns a wait function that returns nil.
func mockWait() func() error {
	return func() error { return nil }
}

// mockWaitError returns a wait function that returns the given error.
func mockWaitError(err error) func() error {
	return func() error { return err }
}

func TestCodexExecutor_Run_Success(t *testing.T) {
	// use content that passes the whitelist filter (priority finding and review section)
	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return mockReader("- [P1] Found issue in foo.go:42\nFull review comments:\n<<<RALPHEX:CODEX_REVIEW_DONE>>>"), mockWait(), nil
		},
	}
	e := &CodexExecutor{cmdRunner: mock}

	result := e.Run(context.Background(), "analyze code")

	require.NoError(t, result.Error)
	assert.Contains(t, result.Output, "Found issue in foo.go:42")
	// signal is detected from raw output
	assert.Equal(t, "<<<RALPHEX:CODEX_REVIEW_DONE>>>", result.Signal)
}

func TestCodexExecutor_Run_StreamsOutput(t *testing.T) {
	// simulates streaming output with headers, bold, priorities, and review
	output := `OpenAI Codex v1.2.3
model: gpt-5
workdir: /tmp/test
sandbox: read-only
Some noise line
**Summary: Found 2 issues**
- [P1] Critical bug in main.go
- [P2] Minor issue in utils.go
Full review comments:
Detailed review line 1
Detailed review line 2
<<<RALPHEX:CODEX_REVIEW_DONE>>>`

	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return mockReader(output), mockWait(), nil
		},
	}

	var streamedLines []string
	e := &CodexExecutor{
		cmdRunner:     mock,
		OutputHandler: func(text string) { streamedLines = append(streamedLines, strings.TrimSuffix(text, "\n")) },
	}

	result := e.Run(context.Background(), "analyze code")

	require.NoError(t, result.Error)

	// verify whitelist filter: headers, bold, priorities, and review section
	assert.Contains(t, streamedLines, "OpenAI Codex v1.2.3", "header should be shown")
	assert.Contains(t, streamedLines, "model: gpt-5", "model header should be shown")
	assert.Contains(t, streamedLines, "workdir: /tmp/test", "workdir header should be shown")
	assert.Contains(t, streamedLines, "sandbox: read-only", "sandbox header should be shown")
	assert.Contains(t, streamedLines, "Summary: Found 2 issues", "bold summary should be shown (stripped)")
	assert.Contains(t, streamedLines, "- [P1] Critical bug in main.go", "priority finding should be shown")
	assert.Contains(t, streamedLines, "- [P2] Minor issue in utils.go", "priority finding should be shown")
	assert.Contains(t, streamedLines, "Full review comments:", "review marker should be shown")
	assert.Contains(t, streamedLines, "Detailed review line 1", "review content should be shown")
	assert.Contains(t, streamedLines, "Detailed review line 2", "review content should be shown")

	// verify noise is filtered
	for _, line := range streamedLines {
		assert.NotContains(t, line, "Some noise line", "noise should be filtered")
	}
}

func TestCodexExecutor_Run_FiltersDuplicateBold(t *testing.T) {
	output := `**Summary: Issue found**
Some noise
**Summary: Issue found**
Another noise
**Summary: Issue found**
- [P1] The actual issue`

	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return mockReader(output), mockWait(), nil
		},
	}

	var streamedLines []string
	e := &CodexExecutor{
		cmdRunner:     mock,
		OutputHandler: func(text string) { streamedLines = append(streamedLines, strings.TrimSuffix(text, "\n")) },
	}

	result := e.Run(context.Background(), "analyze code")

	require.NoError(t, result.Error)

	// count occurrences of bold summary (should be deduplicated to 1)
	count := 0
	for _, line := range streamedLines {
		if strings.Contains(line, "Summary: Issue found") {
			count++
		}
	}
	assert.Equal(t, 1, count, "duplicate bold summaries should be deduplicated")
}

func TestCodexExecutor_Run_StartError(t *testing.T) {
	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return nil, nil, errors.New("command not found")
		},
	}
	e := &CodexExecutor{cmdRunner: mock}

	result := e.Run(context.Background(), "analyze code")

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "start codex")
	assert.Contains(t, result.Error.Error(), "command not found")
}

func TestCodexExecutor_Run_WaitError(t *testing.T) {
	// use whitelisted content (bold summary passes filter)
	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return mockReader("**partial output**"), mockWaitError(errors.New("exit 1")), nil
		},
	}
	e := &CodexExecutor{cmdRunner: mock}

	result := e.Run(context.Background(), "analyze code")

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "codex exited with error")
	assert.Contains(t, result.Output, "partial output") // bold markers stripped
}

func TestCodexExecutor_Run_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return mockReader(""), mockWaitError(context.Canceled), nil
		},
	}
	e := &CodexExecutor{cmdRunner: mock}

	result := e.Run(ctx, "analyze code")

	require.ErrorIs(t, result.Error, context.Canceled)
}

func TestCodexExecutor_Run_DefaultSettings(t *testing.T) {
	var capturedArgs []string
	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, name string, args ...string) (io.Reader, func() error, error) {
			capturedArgs = args
			return mockReader("result"), mockWait(), nil
		},
	}
	e := &CodexExecutor{cmdRunner: mock}

	result := e.Run(context.Background(), "test prompt")

	require.NoError(t, result.Error)

	// verify default settings
	argsStr := strings.Join(capturedArgs, " ")
	assert.Contains(t, argsStr, `model="gpt-5.2-codex"`)
	assert.Contains(t, argsStr, "model_reasoning_effort=xhigh")
	assert.Contains(t, argsStr, "stream_idle_timeout_ms=3600000")
	assert.Contains(t, argsStr, "--sandbox read-only")
}

func TestCodexExecutor_Run_CustomSettings(t *testing.T) {
	var capturedCmd string
	var capturedArgs []string
	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, name string, args ...string) (io.Reader, func() error, error) {
			capturedCmd = name
			capturedArgs = args
			return mockReader("result"), mockWait(), nil
		},
	}
	e := &CodexExecutor{
		cmdRunner:       mock,
		Command:         "custom-codex",
		Model:           "gpt-4o",
		ReasoningEffort: "medium",
		TimeoutMs:       1000,
		Sandbox:         "off",
		ProjectDoc:      "/path/to/doc.md",
	}

	result := e.Run(context.Background(), "test")

	require.NoError(t, result.Error)
	assert.Equal(t, "custom-codex", capturedCmd)

	// verify custom settings
	assert.Equal(t, "exec", capturedArgs[0])
	assert.True(t, slices.Contains(capturedArgs, `model="gpt-4o"`), "expected model setting in args: %v", capturedArgs)

	argsStr := strings.Join(capturedArgs, " ")
	assert.Contains(t, argsStr, "model_reasoning_effort=medium")
	assert.Contains(t, argsStr, "stream_idle_timeout_ms=1000")
	assert.Contains(t, argsStr, "--sandbox off")
	assert.Contains(t, argsStr, `project_doc="/path/to/doc.md"`)
}

func TestCodexExecutor_shouldDisplay_headerPhase(t *testing.T) {
	e := &CodexExecutor{}

	tests := []struct {
		name    string
		line    string
		wantOk  bool
		wantOut string
	}{
		{"codex header", "OpenAI Codex v1.2.3", true, "OpenAI Codex v1.2.3"},
		{"workdir header", "workdir: /tmp/test", true, "workdir: /tmp/test"},
		{"model header", "model: gpt-5", true, "model: gpt-5"},
		{"provider header", "provider: openai", true, "provider: openai"},
		{"approval header", "approval: never", true, "approval: never"},
		{"sandbox header", "sandbox: read-only", true, "sandbox: read-only"},
		{"reasoning effort header", "reasoning effort: xhigh", true, "reasoning effort: xhigh"},
		{"reasoning summaries header", "reasoning summaries: auto", true, "reasoning summaries: auto"},
		{"session id header", "session id: 019bda3c-de4c-7b12-81ed-110d3a0a20e1", true, "session id: 019bda3c-de4c-7b12-81ed-110d3a0a20e1"},
		{"project_doc header", "project_doc: /path/to/doc.md", true, "project_doc: /path/to/doc.md"},
		{"separator line", "--------", true, "--------"},
		{"noise in header", "Running: some command", false, ""},
		{"random noise", "some random noise", false, ""},
		{"empty line", "", false, ""},
		{"whitespace only", "   ", false, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := &codexFilterState{inHeader: true, seenBold: make(map[string]bool)}
			ok, out := e.shouldDisplay(tc.line, state)
			assert.Equal(t, tc.wantOk, ok)
			assert.Equal(t, tc.wantOut, out)
		})
	}
}

func TestCodexExecutor_shouldDisplay_boldSummaries(t *testing.T) {
	e := &CodexExecutor{}
	state := &codexFilterState{inHeader: false, seenBold: make(map[string]bool)}

	// first bold summary should be shown
	ok, out := e.shouldDisplay("**Summary: Found issues**", state)
	assert.True(t, ok)
	assert.Equal(t, "Summary: Found issues", out)

	// duplicate should be filtered
	ok, out = e.shouldDisplay("**Summary: Found issues**", state)
	assert.False(t, ok)
	assert.Empty(t, out)

	// different bold should be shown
	ok, out = e.shouldDisplay("**Another summary**", state)
	assert.True(t, ok)
	assert.Equal(t, "Another summary", out)
}

func TestCodexExecutor_shouldDisplay_priorityFindings(t *testing.T) {
	e := &CodexExecutor{}
	state := &codexFilterState{inHeader: false, seenBold: make(map[string]bool)}

	tests := []struct {
		line    string
		wantOk  bool
		wantOut string
	}{
		{"- [P1] Critical issue", true, "- [P1] Critical issue"},
		{"- [P2] Major issue", true, "- [P2] Major issue"},
		{"- [P3] Minor issue", true, "- [P3] Minor issue"},
		{"- [P4] Low priority", true, "- [P4] Low priority"},
		{"- Some other bullet", false, ""},
		{"[P1] without dash", false, ""},
	}

	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			ok, out := e.shouldDisplay(tc.line, state)
			assert.Equal(t, tc.wantOk, ok)
			assert.Equal(t, tc.wantOut, out)
		})
	}
}

func TestCodexExecutor_shouldDisplay_reviewSection(t *testing.T) {
	e := &CodexExecutor{}
	state := &codexFilterState{inHeader: false, seenBold: make(map[string]bool)}

	// review marker should be shown and enable review mode
	ok, out := e.shouldDisplay("Full review comments:", state)
	assert.True(t, ok)
	assert.Equal(t, "Full review comments:", out)
	assert.True(t, state.inReview, "should enter review mode")

	// everything after should be shown
	ok, out = e.shouldDisplay("This is review content", state)
	assert.True(t, ok)
	assert.Equal(t, "This is review content", out)

	ok, out = e.shouldDisplay("More review content with **bold**", state)
	assert.True(t, ok)
	assert.Equal(t, "More review content with bold", out)

	ok, out = e.shouldDisplay("Even random lines", state)
	assert.True(t, ok)
	assert.Equal(t, "Even random lines", out)
}

func TestCodexExecutor_shouldDisplay_filtersNoise(t *testing.T) {
	e := &CodexExecutor{}
	state := &codexFilterState{inHeader: false, seenBold: make(map[string]bool), lineCount: 20}

	tests := []struct {
		line   string
		wantOk bool
	}{
		{"Thinking...", false},
		{"Processing...", false},
		{"Some random output", false},
		{"diff --git a/file.go b/file.go", false},
		{"+++ b/file.go", false},
		{"--- a/file.go", false},
		{"@@ -1,5 +1,5 @@", false},
		{"Running: test command", false},
		{"Executing: some action", false},
		{"user", false},
		{"thinking", false},
		{"822", false},
	}

	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			ok, _ := e.shouldDisplay(tc.line, state)
			assert.Equal(t, tc.wantOk, ok)
		})
	}
}

func TestCodexExecutor_shouldDisplay_noIssuesFound(t *testing.T) {
	e := &CodexExecutor{}

	tests := []struct {
		name    string
		line    string
		wantOk  bool
		wantOut string
	}{
		{"uppercase", "NO ISSUES FOUND", true, "NO ISSUES FOUND"},
		{"mixed case", "No Issues Found", true, "No Issues Found"},
		{"lowercase", "no issues found", true, "no issues found"},
		{"with prefix", "Result: NO ISSUES FOUND", true, "Result: NO ISSUES FOUND"},
		{"partial no issues", "No issues", true, "No issues"},
		{"in sentence", "There were no issues found in the code", true, "There were no issues found in the code"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := &codexFilterState{inHeader: false, seenBold: make(map[string]bool), lineCount: 30}
			ok, out := e.shouldDisplay(tc.line, state)
			assert.Equal(t, tc.wantOk, ok)
			assert.Equal(t, tc.wantOut, out)
		})
	}
}

func TestCodexExecutor_shouldDisplay_fileLineRef(t *testing.T) {
	e := &CodexExecutor{}
	state := &codexFilterState{inHeader: false, seenBold: make(map[string]bool), lineCount: 30}

	tests := []struct {
		name    string
		line    string
		wantOk  bool
		wantOut string
	}{
		{"go file:line", "pkg/executor/codex.go:123", true, "pkg/executor/codex.go:123"},
		{"go file:line with description", "- pkg/foo.go:45 - missing error check", true, "- pkg/foo.go:45 - missing error check"},
		{"go file:line relative", "./cmd/main.go:10", true, "./cmd/main.go:10"},
		{"ts file:line", "src/components/App.ts:100", true, "src/components/App.ts:100"},
		{"js file:line", "index.js:5", true, "index.js:5"},
		{"py file:line", "script.py:42", true, "script.py:42"},
		{"go without line number", "pkg/foo.go", false, ""},
		{"not a file reference", "some random text", false, ""},
		{"file:line in sentence", "Found issue at pkg/main.go:15 with handling", true, "Found issue at pkg/main.go:15 with handling"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// reset state for each test
			testState := &codexFilterState{inHeader: false, seenBold: make(map[string]bool), lineCount: 30}
			ok, out := e.shouldDisplay(tc.line, testState)
			assert.Equal(t, tc.wantOk, ok, "unexpected ok for: %s", tc.line)
			assert.Equal(t, tc.wantOut, out, "unexpected output for: %s", tc.line)
		})
	}

	_ = state // silence unused warning
}

func TestContainsFileLineRef(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		// common extensions
		{"pkg/foo.go:123", true},
		{"./main.go:1", true},
		{"path/to/file.ts:99", true},
		{"script.py:42", true},
		{"file.rs:100", true},
		{"Main.java:50", true},
		{"file.c:10", true},
		{"file.cpp:20", true},
		{"header.h:5", true},
		{"file.js:1", true},
		// additional extensions (codex review finding)
		{"docs/readme.md:9", true},
		{"config/app.yaml:3", true},
		{"config/settings.yml:15", true},
		{"ui/App.tsx:20", true},
		{"components/Button.jsx:5", true},
		{"styles.css:100", true},
		{"template.html:42", true},
		{"script.sh:7", true},
		{"module.rb:33", true},
		// extensionless files (Makefile, Dockerfile, etc.)
		{"Makefile:12", true},
		{"Dockerfile:5", true},
		{"- Makefile:45 - missing target", true},
		{"See Dockerfile:10 for details", true},
		// negative cases
		{"no file reference", false},
		{"file.go without line", false},
		{"file.go: no number", false},
		{"http://example.com:8080", false}, // url with port, not file:line
		{":123", false},                    // no filename
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := containsFileLineRef(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCodexExecutor_stripBold(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no bold", "plain text", "plain text"},
		{"single bold", "**bold** text", "bold text"},
		{"multiple bold", "**one** and **two**", "one and two"},
		{"nested in text", "before **middle** after", "before middle after"},
		{"unclosed bold", "**unclosed text", "**unclosed text"},
		{"empty bold", "**** empty", " empty"},
	}

	e := &CodexExecutor{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := e.stripBold(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCodexExecutor_Run_FilteredOutput(t *testing.T) {
	// verify that Result.Output contains filtered content (for evaluation prompt)
	// while signal detection uses raw output
	output := `OpenAI Codex v1.2.3
model: gpt-5
Some noise that gets filtered
**Summary**
- [P1] Issue
Full review comments:
Review content
<<<RALPHEX:CODEX_REVIEW_DONE>>>`

	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return mockReader(output), mockWait(), nil
		},
	}

	e := &CodexExecutor{cmdRunner: mock}
	result := e.Run(context.Background(), "analyze code")

	require.NoError(t, result.Error)

	// filtered output should NOT contain noise (it gets filtered)
	assert.NotContains(t, result.Output, "Some noise that gets filtered")
	// filtered output should contain whitelisted content (headers, bold, priorities, review)
	assert.Contains(t, result.Output, "OpenAI Codex v1.2.3")
	assert.Contains(t, result.Output, "Summary") // bold markers stripped
	assert.Contains(t, result.Output, "- [P1] Issue")
	assert.Contains(t, result.Output, "Review content")
	// signal should still be detected from raw output
	assert.Equal(t, "<<<RALPHEX:CODEX_REVIEW_DONE>>>", result.Signal)
}

func TestCodexExecutor_Run_NoOutputHandler(t *testing.T) {
	// verify run works without output handler
	// use whitelisted content (bold summary passes filter)
	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return mockReader("**some output**"), mockWait(), nil
		},
	}

	e := &CodexExecutor{cmdRunner: mock, OutputHandler: nil}
	result := e.Run(context.Background(), "analyze code")

	require.NoError(t, result.Error)
	assert.Contains(t, result.Output, "some output") // bold markers stripped
}

func TestCodexExecutor_processStream_contextCancellation(t *testing.T) {
	// test that context cancellation is detected between line reads
	ctx, cancel := context.WithCancel(context.Background())

	// create a reader that provides one line, then the context is canceled
	pr, pw := io.Pipe()

	go func() {
		_, _ = pw.Write([]byte("line 1\n"))
		cancel() // cancel context after first line written
		_, _ = pw.Write([]byte("line 2\n"))
		pw.Close()
	}()

	e := &CodexExecutor{}
	filtered, raw, _ := e.processStream(ctx, pr)

	// we should get some output (at least partial)
	// the exact behavior depends on timing, but the important thing is no panic/deadlock
	// raw should contain content even if filtered is empty
	assert.True(t, filtered != "" || raw != "", "should have some output")
}

func TestExecCodexRunner_Run(t *testing.T) {
	// test the real runner with a simple command
	runner := &execCodexRunner{}

	// use echo as a test command (writes to stdout, but we can verify the interface works)
	reader, wait, err := runner.Run(context.Background(), "echo", "hello")

	require.NoError(t, err)
	require.NotNil(t, reader)
	require.NotNil(t, wait)

	// wait should complete successfully
	err = wait()
	require.NoError(t, err)
}

func TestExecCodexRunner_Run_CommandNotFound(t *testing.T) {
	runner := &execCodexRunner{}

	// use a command that doesn't exist
	reader, wait, err := runner.Run(context.Background(), "nonexistent-command-12345")

	// should fail at start or wait
	if err != nil {
		assert.Contains(t, err.Error(), "start command")
	} else {
		// if start succeeded, wait should fail
		assert.NotNil(t, reader)
		err = wait()
		assert.Error(t, err)
	}
}

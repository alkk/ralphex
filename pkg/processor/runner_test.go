package processor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/executor"
	"github.com/umputun/ralphex/pkg/processor/mocks"
	"github.com/umputun/ralphex/pkg/progress"
)

// testAppConfig loads config with embedded defaults for testing.
func testAppConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	require.NoError(t, err)
	return cfg
}

// newMockExecutor creates a mock executor with predefined results.
func newMockExecutor(results []executor.Result) *mocks.ExecutorMock {
	idx := 0
	return &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			if idx >= len(results) {
				return executor.Result{Error: errors.New("no more mock results")}
			}
			result := results[idx]
			idx++
			return result
		},
	}
}

// newMockLogger creates a mock logger with no-op implementations.
func newMockLogger(path string) *mocks.LoggerMock {
	return &mocks.LoggerMock{
		SetPhaseFunc:     func(_ progress.Phase) {},
		PrintFunc:        func(_ string, _ ...any) {},
		PrintRawFunc:     func(_ string, _ ...any) {},
		PrintSectionFunc: func(_ string) {},
		PrintAlignedFunc: func(_ string) {},
		PathFunc:         func() string { return path },
	}
}

func TestRunner_Run_UnknownMode(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	r := NewWithExecutors(Config{Mode: "invalid"}, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown mode")
}

func TestRunner_RunFull_NoPlanFile(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	r := NewWithExecutors(Config{Mode: ModeFull}, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan file required")
}

func TestRunner_RunFull_Success(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	// all tasks complete - no [ ] checkboxes
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [x] Task 1"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "task done", Signal: SignalCompleted},    // task phase completes
		{Output: "review done", Signal: SignalReviewDone}, // first review
		{Output: "review done", Signal: SignalReviewDone}, // pre-codex review loop
		{Output: "done", Signal: SignalCodexDone},         // codex evaluation
		{Output: "review done", Signal: SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue in foo.go"}, // codex finds issues
	})

	cfg := Config{Mode: ModeFull, PlanFile: planFile, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 1)
}

func TestRunner_RunFull_NoCodexFindings(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [x] Task 1"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "task done", Signal: SignalCompleted},
		{Output: "review done", Signal: SignalReviewDone}, // first review
		{Output: "review done", Signal: SignalReviewDone}, // pre-codex review loop
		{Output: "review done", Signal: SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: ""}, // codex finds nothing
	})

	cfg := Config{Mode: ModeFull, PlanFile: planFile, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
}

func TestRunner_RunReviewOnly_Success(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: SignalReviewDone}, // first review
		{Output: "review done", Signal: SignalReviewDone}, // pre-codex review loop
		{Output: "done", Signal: SignalCodexDone},         // codex evaluation
		{Output: "review done", Signal: SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue"},
	})

	cfg := Config{Mode: ModeReview, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 1)
}

func TestRunner_RunCodexOnly_Success(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "done", Signal: SignalCodexDone},         // codex evaluation
		{Output: "review done", Signal: SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue"},
	})

	cfg := Config{Mode: ModeCodexOnly, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 1)
}

func TestRunner_RunCodexOnly_NoFindings(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: ""}, // no findings
	})

	cfg := Config{Mode: ModeCodexOnly, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
}

func TestRunner_CodexDisabled_SkipsCodexPhase(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor(nil)

	cfg := Config{Mode: ModeCodexOnly, MaxIterations: 50, CodexEnabled: false, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Empty(t, codex.RunCalls(), "codex should not be called when disabled")
}

func TestRunner_TaskPhase_FailedSignal(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "error", Signal: SignalFailed}, // first try
		{Output: "error", Signal: SignalFailed}, // retry
	})
	codex := newMockExecutor(nil)

	cfg := Config{Mode: ModeFull, PlanFile: planFile, MaxIterations: 10, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAILED signal")
}

func TestRunner_TaskPhase_MaxIterations(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [ ] Task 1"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "working..."},
		{Output: "still working..."},
		{Output: "more work..."},
	})
	codex := newMockExecutor(nil)

	cfg := Config{Mode: ModeFull, PlanFile: planFile, MaxIterations: 3, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "max iterations")
}

func TestRunner_TaskPhase_ContextCanceled(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	log := newMockLogger("progress.txt")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	cfg := Config{Mode: ModeFull, PlanFile: planFile, MaxIterations: 10, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(ctx)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRunner_ClaudeReview_FailedSignal(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "error", Signal: SignalFailed},
	})
	codex := newMockExecutor(nil)

	cfg := Config{Mode: ModeReview, MaxIterations: 50, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAILED signal")
}

func TestRunner_CodexPhase_Error(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: SignalReviewDone}, // first review
		{Output: "review done", Signal: SignalReviewDone}, // pre-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Error: errors.New("codex error")},
	})

	cfg := Config{Mode: ModeReview, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "codex")
	assert.Len(t, codex.RunCalls(), 1, "codex should be called once")
}

func TestRunner_ClaudeExecution_Error(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Error: errors.New("claude error")},
	})
	codex := newMockExecutor(nil)

	cfg := Config{Mode: ModeFull, PlanFile: planFile, MaxIterations: 10, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "claude execution")
}

func TestRunner_hasUncompletedTasks(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("with uncompleted tasks", func(t *testing.T) {
		planFile := filepath.Join(tmpDir, "uncompleted.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [ ] Task 1\n- [x] Task 2"), 0o600))

		r := &Runner{cfg: Config{PlanFile: planFile}}
		assert.True(t, r.hasUncompletedTasks())
	})

	t.Run("all tasks completed", func(t *testing.T) {
		planFile := filepath.Join(tmpDir, "completed.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [x] Task 1\n- [x] Task 2"), 0o600))

		r := &Runner{cfg: Config{PlanFile: planFile}}
		assert.False(t, r.hasUncompletedTasks())
	})

	t.Run("missing file", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: "/nonexistent/file.md"}}
		assert.True(t, r.hasUncompletedTasks())
	})
}

func TestRunner_ConfigValues(t *testing.T) {
	t.Run("default iteration delay", func(t *testing.T) {
		log := newMockLogger("")
		claude := newMockExecutor(nil)
		codex := newMockExecutor(nil)

		r := NewWithExecutors(Config{}, log, claude, codex)
		assert.Equal(t, defaultIterationDelay, r.iterationDelay)
	})

	t.Run("custom iteration delay from config", func(t *testing.T) {
		log := newMockLogger("")
		claude := newMockExecutor(nil)
		codex := newMockExecutor(nil)

		r := NewWithExecutors(Config{IterationDelayMs: 5000}, log, claude, codex)
		assert.Equal(t, 5*time.Second, r.iterationDelay)
	})

	t.Run("default task retry count", func(t *testing.T) {
		log := newMockLogger("")
		claude := newMockExecutor(nil)
		codex := newMockExecutor(nil)

		r := NewWithExecutors(Config{}, log, claude, codex)
		assert.Equal(t, 1, r.taskRetryCount)
	})

	t.Run("custom task retry count from config", func(t *testing.T) {
		log := newMockLogger("")
		claude := newMockExecutor(nil)
		codex := newMockExecutor(nil)

		r := NewWithExecutors(Config{TaskRetryCount: 3}, log, claude, codex)
		assert.Equal(t, 3, r.taskRetryCount)
	})
}

func TestRunner_TaskRetryCount_UsedCorrectly(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [ ] Task 1"), 0o600))

	log := newMockLogger("progress.txt")

	// test with TaskRetryCount=2 - should retry twice before failing
	claude := newMockExecutor([]executor.Result{
		{Output: "error", Signal: SignalFailed}, // first try
		{Output: "error", Signal: SignalFailed}, // retry 1
		{Output: "error", Signal: SignalFailed}, // retry 2
	})
	codex := newMockExecutor(nil)

	cfg := Config{
		Mode:           ModeFull,
		PlanFile:       planFile,
		MaxIterations:  10,
		TaskRetryCount: 2,
		// use 1ms delay for faster tests
		IterationDelayMs: 1,
		AppConfig:        testAppConfig(t),
	}
	r := NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAILED signal")
	// should have tried 3 times: initial + 2 retries
	assert.Len(t, claude.RunCalls(), 3)
}

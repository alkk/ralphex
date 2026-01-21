package executor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
)

// execCodexRunner is the default command runner using os/exec for codex.
// codex outputs to stderr for streaming, unlike claude which uses stdout.
type execCodexRunner struct{}

func (r *execCodexRunner) Run(ctx context.Context, name string, args ...string) (io.Reader, func() error, error) {
	cmd := exec.CommandContext(ctx, name, args...)

	// codex outputs to stderr for streaming
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start command: %w", err)
	}

	return stderr, cmd.Wait, nil
}

// CodexExecutor runs codex CLI commands and filters output.
type CodexExecutor struct {
	Command         string            // command to execute, defaults to "codex"
	Model           string            // model to use, defaults to gpt-5.2-codex
	ReasoningEffort string            // reasoning effort level, defaults to "xhigh"
	TimeoutMs       int               // stream idle timeout in ms, defaults to 3600000
	Sandbox         string            // sandbox mode, defaults to "read-only"
	ProjectDoc      string            // path to project documentation file
	OutputHandler   func(text string) // called for each filtered output line in real-time
	Debug           bool              // enable debug output
	cmdRunner       CommandRunner     // for testing, nil uses default
}

// codexFilterState tracks whitelist filter state machine.
type codexFilterState struct {
	inHeader  bool            // true at start, false when non-header seen
	inReview  bool            // true after "Full review comments:"
	seenBold  map[string]bool // dedupe bold summaries
	lineCount int             // track header lines
}

// Run executes codex CLI with the given prompt and returns filtered output.
// Output is streamed line-by-line to OutputHandler in real-time.
func (e *CodexExecutor) Run(ctx context.Context, prompt string) Result {
	cmd := e.Command
	if cmd == "" {
		cmd = "codex"
	}

	model := e.Model
	if model == "" {
		model = "gpt-5.2-codex"
	}

	reasoningEffort := e.ReasoningEffort
	if reasoningEffort == "" {
		reasoningEffort = "xhigh"
	}

	timeoutMs := e.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 3600000
	}

	sandbox := e.Sandbox
	if sandbox == "" {
		sandbox = "read-only"
	}

	args := []string{
		"exec",
		"--sandbox", sandbox,
		"-c", fmt.Sprintf("model=%q", model),
		"-c", "model_reasoning_effort=" + reasoningEffort,
		"-c", fmt.Sprintf("stream_idle_timeout_ms=%d", timeoutMs),
	}

	if e.ProjectDoc != "" {
		args = append(args, "-c", fmt.Sprintf("project_doc=%q", e.ProjectDoc))
	}

	args = append(args, prompt)

	runner := e.cmdRunner
	if runner == nil {
		runner = &execCodexRunner{}
	}

	stderr, wait, err := runner.Run(ctx, cmd, args...)
	if err != nil {
		return Result{Error: fmt.Errorf("start codex: %w", err)}
	}

	// stream and filter output
	filteredOutput, rawOutput, streamErr := e.processStream(ctx, stderr)

	// wait for command completion
	waitErr := wait()

	// determine final error
	var finalErr error
	if streamErr != nil {
		finalErr = streamErr
	} else if waitErr != nil {
		if ctx.Err() != nil {
			finalErr = ctx.Err()
		} else {
			finalErr = fmt.Errorf("codex exited with error: %w", waitErr)
		}
	}

	// detect signal in raw output (includes all content)
	signal := detectSignal(rawOutput)

	// return filtered output for evaluation prompt
	return Result{Output: filteredOutput, Signal: signal, Error: finalErr}
}

// processStream reads stderr line-by-line, filters, and calls OutputHandler.
// returns filtered output (for evaluation prompt) and raw output (for signal detection).
func (e *CodexExecutor) processStream(ctx context.Context, r io.Reader) (filtered, raw string, err error) {
	var filteredOutput, rawOutput strings.Builder
	state := &codexFilterState{
		inHeader: true,
		seenBold: make(map[string]bool),
	}

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		// check for context cancellation
		select {
		case <-ctx.Done():
			return filteredOutput.String(), rawOutput.String(), ctx.Err()
		default:
		}

		line := scanner.Text()
		rawOutput.WriteString(line + "\n")

		// apply whitelist filter
		show, filteredLine := e.shouldDisplay(line, state)
		if show {
			filteredOutput.WriteString(filteredLine + "\n")
			if e.OutputHandler != nil {
				e.OutputHandler(filteredLine + "\n")
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return filteredOutput.String(), rawOutput.String(), fmt.Errorf("read stream: %w", err)
	}

	return filteredOutput.String(), rawOutput.String(), nil
}

// codexHeaderPrefixes are displayed during the header phase (whitelist).
var codexHeaderPrefixes = []string{
	"OpenAI Codex",
	"workdir:",
	"model:",
	"provider:",
	"approval:",
	"sandbox:",
	"reasoning effort:",
	"reasoning summaries:",
	"session id:",
	"project_doc:",
	"--------", // separator line
}

// shouldDisplay implements whitelist filter for codex output.
// Returns whether to display the line and the cleaned version.
func (e *CodexExecutor) shouldDisplay(line string, state *codexFilterState) (bool, string) {
	s := strings.TrimSpace(line)
	if s == "" {
		return false, ""
	}

	state.lineCount++

	// review section marker: show it and everything after
	if strings.Contains(s, "Full review comments:") {
		state.inReview = true
		state.inHeader = false
		return true, line
	}
	if state.inReview {
		return true, e.stripBold(line)
	}

	// "NO ISSUES FOUND" - explicit clean result from codex
	upper := strings.ToUpper(s)
	if strings.Contains(upper, "NO ISSUES FOUND") || strings.Contains(upper, "NO ISSUES") {
		state.inHeader = false
		return true, line
	}

	// bold summaries: show (deduplicated)
	if strings.HasPrefix(s, "**") {
		state.inHeader = false
		cleaned := e.stripBold(s)
		if state.seenBold[cleaned] {
			return false, ""
		}
		state.seenBold[cleaned] = true
		return true, cleaned
	}

	// priority findings: show
	if strings.HasPrefix(s, "- [P") {
		state.inHeader = false
		return true, e.stripBold(line)
	}

	// file:line references (e.g., "pkg/foo/bar.go:123" or "- pkg/foo.go:45 - description")
	// this matches the format requested in the codex prompt
	if containsFileLineRef(s) {
		state.inHeader = false
		return true, e.stripBold(line)
	}

	// header: show only specific prefixes (first ~20 lines)
	if state.inHeader && state.lineCount <= 20 {
		for _, prefix := range codexHeaderPrefixes {
			if strings.HasPrefix(s, prefix) {
				return true, line
			}
		}
		// still in header zone but not a header prefix - continue
		return false, ""
	}

	// exit header phase after threshold
	if state.inHeader && state.lineCount > 20 {
		state.inHeader = false
	}

	// everything else is filtered (commands, diffs, tool output, etc.)
	return false, ""
}

// fileLineRefPattern matches file:line references like "pkg/foo.go:123", "Makefile:45",
// "./path/file.ts:12", "docs/readme.md:9". Handles both files with extensions and
// extensionless files (Makefile, Dockerfile, etc.).
// excludes URLs by requiring no // before the match.
var fileLineRefPattern = regexp.MustCompile(`(?:^|[^a-zA-Z0-9/])([a-zA-Z0-9_./-]+[a-zA-Z0-9_]):(\d+)`)

// containsFileLineRef checks if a line contains a file:line reference pattern.
// matches patterns like "pkg/foo.go:123", "Makefile:45", "./path/file.ts:12".
// avoids false positives on URLs like "http://example.com:8080".
func containsFileLineRef(s string) bool {
	// quick check for URL patterns to avoid false positives
	if strings.Contains(s, "://") {
		// remove URL portion and check remaining text
		s = urlPattern.ReplaceAllString(s, " ")
	}
	return fileLineRefPattern.MatchString(s)
}

// urlPattern matches common URL patterns to filter them out
var urlPattern = regexp.MustCompile(`https?://\S+`)

// stripBold removes markdown bold markers (**text**) from text.
func (e *CodexExecutor) stripBold(s string) string {
	// replace **text** with text
	result := s
	for {
		start := strings.Index(result, "**")
		if start == -1 {
			break
		}
		end := strings.Index(result[start+2:], "**")
		if end == -1 {
			break
		}
		// remove both markers
		result = result[:start] + result[start+2:start+2+end] + result[start+2+end+2:]
	}
	return result
}

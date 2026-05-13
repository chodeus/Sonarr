package customscript

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ErrSendFailed wraps a process-spawn failure. Mirrors the C# catch-
// all at `CustomScript.cs:349-353`.
var ErrSendFailed = errors.New("customscript: script execution failed")

// ErrNonZeroExit is the distinguished sentinel for a non-zero exit
// status. Mirrors C# `:344-347` "Script exited with code: X".
type ErrNonZeroExit struct{ Code int }

// Error formats the non-zero exit.
func (e *ErrNonZeroExit) Error() string {
	return fmt.Sprintf("customscript: script exited with code %d", e.Code)
}

// scriptRunner is the narrow exec surface the proxy needs. Production
// uses [defaultRunner] which wraps os/exec. Tests inject a stub that
// records the env vars without spawning.
type scriptRunner interface {
	Run(ctx context.Context, path string, env map[string]string) (stdout, stderr string, exitCode int, err error)
}

// defaultRunner is the production scriptRunner — wraps os/exec.
type defaultRunner struct{}

// Run invokes path with the given env map merged onto the parent
// process env. Mirrors C# `IProcessProvider.StartAndCapture` at
// `CustomScript.cs:363`.
func (defaultRunner) Run(ctx context.Context, path string, env map[string]string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, path)
	cmd.Env = append(os.Environ(), envSlice(env)...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		}
	}
	return stdout.String(), stderr.String(), exitCode, err
}

// runner is the package-global scriptRunner — replaced by tests via
// [SetRunnerForTest].
var runner scriptRunner = defaultRunner{}

// SetRunnerForTest swaps the package scriptRunner. Passing nil
// restores the production [defaultRunner]. Intended only for tests.
func SetRunnerForTest(r scriptRunner) {
	if r == nil {
		runner = defaultRunner{}
		return
	}
	runner = r
}

// execScript fires the configured script with the supplied env vars.
// Non-zero exit returns [ErrNonZeroExit]; other failures (spawn /
// not-found / permission) return [ErrSendFailed] wrapped.
func execScript(ctx context.Context, settings *Settings, env map[string]string) error {
	if strings.TrimSpace(settings.Path) == "" {
		return fmt.Errorf("%w: empty script path", ErrSendFailed)
	}
	_, _, exitCode, err := runner.Run(ctx, settings.Path, env)
	if err != nil {
		// exec.ExitError surfaces both spawn-OK + non-zero-exit. When
		// exitCode != 0 we prefer the typed sentinel; otherwise the
		// error is a spawn-level failure (not-found / permission).
		if exitCode != 0 {
			return &ErrNonZeroExit{Code: exitCode}
		}
		return fmt.Errorf("%w: %w", ErrSendFailed, err)
	}
	if exitCode != 0 {
		return &ErrNonZeroExit{Code: exitCode}
	}
	return nil
}

// envSlice converts a map to KEY=VALUE entries.
func envSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

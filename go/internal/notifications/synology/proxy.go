package synology

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// synoindexPath is the synoindex CLI Sonarr execs. Mirrors C# `SynoIndexPath`
// at `SynologyIndexerProxy.cs:20`. Mutable via [SetExecPathForTest].
var synoindexPath = "/usr/syno/bin/synoindex"

// SetExecPathForTest swaps the synoindex CLI path. Intended only for
// tests that point the proxy at a stub script in a temp dir.
func SetExecPathForTest(p string) { synoindexPath = p }

// commandRunner is the narrow exec surface the proxy needs. Production
// uses [defaultRunner] which shells out via os/exec.
type commandRunner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr string, err error)
}

type defaultRunner struct{}

// Run executes name with args and captures both streams.
func (defaultRunner) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// runner is the package-global commandRunner — replaced by tests with
// a stub via [SetRunnerForTest].
var runner commandRunner = defaultRunner{}

// SetRunnerForTest swaps the package commandRunner. Passing nil
// restores the production [defaultRunner]. Intended only for tests
// that need to assert on the synoindex command line without actually
// execing a binary.
func SetRunnerForTest(r commandRunner) {
	if r == nil {
		runner = defaultRunner{}
		return
	}
	runner = r
}

// ErrNotLinux is returned by Test() on non-Linux hosts — mirrors C#
// `:100-102` "Synology indexer requires Linux".
var ErrNotLinux = errors.New("synology: indexer requires Linux")

// ErrSendFailed wraps a transport-level failure from synoindex.
// Mirrors C# `SynologyException`.
var ErrSendFailed = errors.New("synology: synoindex failed")

// addFile execs `synoindex -a <path>`. Mirrors C# `:45-48`.
func addFile(ctx context.Context, path string) error {
	return execIndex(ctx, "-a", path)
}

// deleteFile execs `synoindex -d <path>`. Mirrors C# `:50-53`.
func deleteFile(ctx context.Context, path string) error {
	return execIndex(ctx, "-d", path)
}

// updateFolder execs `synoindex -R <folder>`. Mirrors C# `:65-68`.
func updateFolder(ctx context.Context, folder string) error {
	return execIndex(ctx, "-R", folder)
}

// deleteFolder execs `synoindex -D <folder>`. Mirrors C# `:60-63`.
func deleteFolder(ctx context.Context, folder string) error {
	return execIndex(ctx, "-D", folder)
}

// testHelp execs `synoindex --help` and reports availability. Mirrors
// C# `:31-43`.
func testHelp(ctx context.Context) error {
	_, _, err := runner.Run(ctx, synoindexPath, "--help")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSendFailed, err)
	}
	return nil
}

// execIndex is the shared exec helper. Mirrors C# `ExecuteCommand` at
// `:75-88`. The C# code throws when stdout OR stderr is non-empty; the
// Go port maps either to ErrSendFailed.
func execIndex(ctx context.Context, flag, path string) error {
	stdout, stderr, err := runner.Run(ctx, synoindexPath, flag, path)
	if err != nil {
		return fmt.Errorf("%w: %w (stderr=%s)", ErrSendFailed, err, strings.TrimSpace(stderr))
	}
	if strings.TrimSpace(stdout) != "" {
		return fmt.Errorf("%w: synoindex returned an error: %s", ErrSendFailed, strings.TrimSpace(stdout))
	}
	if strings.TrimSpace(stderr) != "" {
		return fmt.Errorf("%w: synoindex returned an error: %s", ErrSendFailed, strings.TrimSpace(stderr))
	}
	return nil
}

// isLinux returns true when the host OS is Linux. Mirrors C# `OsInfo.IsLinux`.
func isLinux() bool { return runtime.GOOS == "linux" }

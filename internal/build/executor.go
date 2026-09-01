package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// MergeEnv returns a copy of env with the given key=value overrides applied.
// Existing entries for overridden keys are REPLACED (appending duplicates would
// leave the original first-match entry effective in most shells).
func MergeEnv(env []string, overrides map[string]string) []string {
	out := make([]string, 0, len(env)+len(overrides))
	for _, kv := range env {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if _, replaced := overrides[k]; replaced {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

// RunCommandCtx starts cmd and waits for it to finish. The command runs in its
// own process group (Setpgid), so when ctx is canceled the WHOLE group is
// SIGKILLed -- killing only the direct child (the sh wrapper) would orphan its
// grandchildren (mvn, node, ...), the same process-tree problem Stop solves
// for services. cmd must not have been started.
func RunCommandCtx(ctx context.Context, cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return ctx.Err()
	}
}

// ExecuteBuild runs the given command in the workspace directory via `sh -c`,
// so shell semantics (&&, |, >, $VAR, etc.) work. It sets JAVA_HOME if a
// .java-version file exists in the workspace. The command is aborted (whole
// process group killed) when ctx is canceled or times out. out receives the
// command output.
func ExecuteBuild(ctx context.Context, workspace, command string, out io.Writer) error {
	if command == "" {
		return fmt.Errorf("build command is empty")
	}

	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = workspace
	cmd.Stdout = out
	cmd.Stderr = out

	// Auto-detect Java version from .java-version file
	if javaVersion := DetectJavaVersion(workspace); javaVersion != "" {
		if javaHome := FindJavaHome(javaVersion); javaHome != "" {
			cmd.Env = MergeEnv(os.Environ(), map[string]string{
				"JAVA_HOME": javaHome,
				"PATH":      filepath.Join(javaHome, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
			})
		}
	}

	fmt.Fprintf(out, "[build] executing: %s\n", command)
	if err := RunCommandCtx(ctx, cmd); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("build aborted (timeout or canceled): %w", ctx.Err())
		}
		return fmt.Errorf("build failed: %w", err)
	}
	fmt.Fprintln(out, "[build] build completed successfully")
	return nil
}

// DetectJavaVersion reads .java-version file from workspace ("" if absent).
func DetectJavaVersion(workspace string) string {
	versionFile := filepath.Join(workspace, ".java-version")
	data, err := os.ReadFile(versionFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

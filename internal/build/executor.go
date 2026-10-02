package build

import (
	"context"
	"os/exec"
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

package components

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"

	"github.com/auto-deployer/auto-deployer/internal/build"
)

// stdoutLimit 结果变量里 stdout/stderr 的截断上限：完整输出已在 Out（日志）里，
// 参数集只需要可引用的片段，防构建日志撑爆内存。
const stdoutLimit = 1024 * 1024

func init() { Register("shell", Shell{}) }

// Shell 在 deployd 本机执行 shell 命令（要操作远程机器请自己写 ssh user@host '...'）。
// sh 支持单行与多行 YAML 块（Jenkins sh ''' 的对应物）。
type Shell struct{}

func (Shell) Run(ctx context.Context, req Request) (Results, error) {
	sh := asString(req.Params, "sh")
	if sh == "" {
		return nil, fmt.Errorf("shell 组件缺少必填参数 sh")
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command("sh", "-c", sh)
	if cwd := asString(req.Params, "cwd"); cwd != "" {
		cmd.Dir = cwd
	}
	// 边执行边写日志（Out），同时在内存留一份做结果变量
	cmd.Stdout = io.MultiWriter(&stdout, req.Out)
	cmd.Stderr = io.MultiWriter(&stderr, req.Out)
	if envParam, ok := req.Params["env"].(map[string]any); ok && len(envParam) > 0 {
		overrides := make(map[string]string, len(envParam))
		for k, v := range envParam {
			overrides[k] = fmt.Sprint(v)
		}
		cmd.Env = build.MergeEnv(os.Environ(), overrides)
	}

	err := build.RunCommandCtx(ctx, cmd) // Setpgid：ctx 取消杀整个进程组

	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode() // 信号致死 = -1
	}
	res := Results{
		"exit_code": strconv.Itoa(code),
		"stdout":    truncate(stdout.String(), stdoutLimit),
		"stderr":    truncate(stderr.String(), stdoutLimit),
	}
	if err != nil {
		if ctx.Err() != nil {
			return res, fmt.Errorf("shell 被中止（超时或取消）: %w", ctx.Err())
		}
		return res, fmt.Errorf("exit_code=%d", code)
	}
	return res, nil
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit]
}

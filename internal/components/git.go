package components

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/auto-deployer/auto-deployer/internal/build"
)

func init() { Register("git", Git{}) }

// Git 拉取代码到 workspace：快路径（fetch --force + checkout -f + reset --hard）
// 失败自动回退 Jenkins 式干净克隆（复用 build.Fetch 的全部语义，含 HTTPS→SSH）。
type Git struct{}

func (Git) Run(ctx context.Context, req Request) (Results, error) {
	url := asString(req.Params, "url")
	branch := asString(req.Params, "branch")
	ws := asString(req.Params, "workspace")
	if url == "" || branch == "" || ws == "" {
		return nil, fmt.Errorf("git 组件需要 url/branch/workspace 参数")
	}

	// SSH key 只需存在（已有则秒回）；新机器上自动生成并提示用户配公钥
	keyFile, _, _, err := build.EnsureSSHKey()
	if err != nil {
		return nil, fmt.Errorf("确保 SSH 密钥: %w", err)
	}

	before := headOf(ws)
	if err := build.Fetch(ctx, url, keyFile, branch, ws, req.Out); err != nil {
		return Results{}, err
	}
	after := headOf(ws)

	changed := "true"
	if before != "" && before == after {
		changed = "false"
	}
	return Results{
		"commit":  after,
		"changed": changed,
		"branch":  branch,
	}, nil
}

// headOf 返回 workspace 当前 HEAD（非 git 仓库返回空串）。
func headOf(ws string) string {
	out, err := exec.Command("git", "-C", ws, "log", "-1", "--format=%H").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

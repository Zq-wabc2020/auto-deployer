//go:build e2e

package test

// 轻量 e2e：本地裸仓库 + 临时 HOME + 真 daemon 进程 + 真 HTTP webhook。
// 跳过条件：git/java/mvn 不可用。跑法：go test -tags e2e ./test/ -v -timeout 120s

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// e2eRunState 是 internal/runstate.RunState 的镜像，用于解析 ~/.deployd/run/<name>.status。
type e2eStageState struct {
	Name     string `json:"name"`
	State    string `json:"state"`
	Duration string `json:"duration,omitempty"`
}

type e2eRunState struct {
	State         string          `json:"state"`
	Trigger       string          `json:"trigger"`
	PID           int             `json:"pid,omitempty"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
	Commit        string          `json:"commit,omitempty"`
	Branch        string          `json:"branch,omitempty"`
	FailedStage   string          `json:"failed_stage,omitempty"`
	FailureReason string          `json:"failure_reason,omitempty"`
	Stages        []e2eStageState `json:"stages"`
}

var deploydBin string

func TestMain(m *testing.M) {
	for _, tool := range []string{"git", "java", "mvn"} {
		if _, err := exec.LookPath(tool); err != nil {
			fmt.Fprintf(os.Stderr, "e2e: skip: %s 不在 PATH 上\n", tool)
			os.Exit(0)
		}
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		fmt.Fprintln(os.Stderr, "e2e: 无法定位本文件路径")
		os.Exit(1)
	}
	repoRoot := filepath.Dir(filepath.Dir(file))
	binDir, err := os.MkdirTemp("", "deployd-e2e-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: 创建临时 bin 目录失败:", err)
		os.Exit(1)
	}
	deploydBin = filepath.Join(binDir, "deployd")
	cmd := exec.Command("go", "build", "-o", deploydBin, ".")
	cmd.Dir = repoRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "e2e: go build 失败:", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(binDir)
	os.Exit(code)
}

func TestE2E_WebhookToPipeline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// 本地裸仓库：setupDir 里提交一次，再 clone --bare。
	setupDir := t.TempDir()
	bareDir := t.TempDir()
	runGit(t, setupDir, "init", "-b", "main")
	runGit(t, setupDir, "config", "user.email", "dev@example.com")
	runGit(t, setupDir, "config", "user.name", "Dev")
	if err := os.WriteFile(filepath.Join(setupDir, "README.md"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, setupDir, "add", ".")
	runGit(t, setupDir, "commit", "-m", "init")
	runGit(t, setupDir, "clone", "--bare", setupDir, bareDir)
	sha := strings.TrimSpace(gitOut(t, setupDir, "rev-parse", "HEAD"))

	// 空闲端口。
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	// config.yaml。
	appWS := t.TempDir()
	cfg := fmt.Sprintf(`server:
  host: "127.0.0.1"
  port: %d
pipelines:
  - name: "my-app"
    workspace: "%s"
    stages:
      - name: 拉取代码
        type: git
        params:
          url: "%s"
          branch: ["main"]
      - name: 构建
        type: shell
        params:
          sh: "echo built > ${system.workspace}/marker"
      - name: 读取
        type: shell
        params:
          sh: "cat marker"
      - name: 清理
        type: cleanup
        when: always
        params:
          keep: [".git"]
`, port, appWS, bareDir)
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}

	// 启动 daemon（foreground）。
	daemonCmd := exec.Command(deploydBin, "start", "-c", cfgPath, "--no-fork")
	daemonCmd.Stdout = os.Stdout
	daemonCmd.Stderr = os.Stderr
	if err := daemonCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if daemonCmd.Process != nil {
			_ = daemonCmd.Process.Signal(syscall.SIGTERM)
			_ = daemonCmd.Wait()
		}
	})

	baseURL := fmt.Sprintf("http://127.0.0.1:%d/webhook", port)
	payload := fmt.Sprintf(`{"ref":"refs/heads/main","repository":{"clone_url":"%s"},"commits":[{"id":"%s","message":"init","author":{"email":"dev@example.com","name":"Dev"}}]}`,
		bareDir, sha)
	statusPath := filepath.Join(home, ".deployd", "run", "my-app.status")
	logPath := filepath.Join(home, ".deployd", "services", "my-app.log")

	// 第一次运行：webhook → 队列 → 流水线全绿。
	postWebhook(t, baseURL, payload)
	first := waitStatus(t, statusPath, 30*time.Second, "success")
	if !allStagesSuccess(first.Stages) {
		t.Fatalf("第一次运行存在未成功阶段: %+v", first.Stages)
	}
	assertStageOrder(t, first, []string{"拉取代码", "构建", "读取", "清理"})

	svcLog := readFile(t, logPath)
	if !strings.Contains(svcLog, "--- [构建] shell ---") {
		t.Fatalf("服务日志缺少构建阶段分隔行；服务日志：\n%s", svcLog)
	}
	if !strings.Contains(svcLog, "built") {
		t.Fatalf("服务日志缺少 shell stdout(built)；服务日志：\n%s", svcLog)
	}

	// 第二次：锁被持有期间再打一发，验证调度合并。
	postWebhook(t, baseURL, payload)
	st := waitNewRun(t, statusPath, first.StartedAt, 30*time.Second)
	if st.State != "running" && st.State != "success" {
		t.Fatalf("第二次运行意外状态: %+v", st)
	}
	postWebhook(t, baseURL, payload)
	second := waitStatus(t, statusPath, 30*time.Second, "success")
	if !allStagesSuccess(second.Stages) {
		t.Fatalf("第二次运行存在未成功阶段: %+v", second.Stages)
	}

	svcLog = readFile(t, logPath)
	daemonLog := readFile(t, filepath.Join(home, ".deployd", "deployd.log"))
	if !strings.Contains(svcLog, "合并") && !strings.Contains(daemonLog, "合并") {
		t.Fatalf("日志缺少调度合并行；服务日志尾部：\n%s\n\n部署日志尾部：\n%s", tail(svcLog), tail(daemonLog))
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v 失败: %v\n%s", args, err, out)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v 失败: %v\n%s", args, err, out)
	}
	return string(out)
}

func postWebhook(t *testing.T, baseURL, body string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	client := &http.Client{Timeout: 15 * time.Second}
	for {
		req, err := http.NewRequest(http.MethodPost, baseURL, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-GitHub-Event", "push")
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("webhook 返回 %d", resp.StatusCode)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("webhook 请求失败: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func waitStatus(t *testing.T, statusPath string, timeout time.Duration, accept ...string) e2eRunState {
	t.Helper()
	acceptSet := make(map[string]bool, len(accept))
	for _, a := range accept {
		acceptSet[a] = true
	}
	deadline := time.Now().Add(timeout)
	var last e2eRunState
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(statusPath)
		if err == nil && json.Unmarshal(data, &last) == nil && acceptSet[last.State] {
			return last
		}
		time.Sleep(500 * time.Millisecond)
	}
	data, _ := os.ReadFile(statusPath)
	t.Fatalf("等待状态 %v 超时（%v）；当前状态文件：%s", accept, timeout, string(data))
	return e2eRunState{}
}

// waitNewRun 等待状态文件出现 StartedAt 晚于 prev 的新运行。
func waitNewRun(t *testing.T, statusPath string, prev time.Time, timeout time.Duration) e2eRunState {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(statusPath)
		if err == nil {
			var st e2eRunState
			if json.Unmarshal(data, &st) == nil && st.StartedAt.After(prev) {
				return st
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	data, _ := os.ReadFile(statusPath)
	t.Fatalf("等待新运行超时（%v）；当前状态文件：%s", timeout, string(data))
	return e2eRunState{}
}

func allStagesSuccess(stages []e2eStageState) bool {
	if len(stages) == 0 {
		return false
	}
	for _, s := range stages {
		if s.State != "success" {
			return false
		}
	}
	return true
}

func assertStageOrder(t *testing.T, st e2eRunState, want []string) {
	t.Helper()
	if len(st.Stages) != len(want) {
		t.Fatalf("阶段数量不符: got %d want %d (%+v)", len(st.Stages), len(want), st.Stages)
	}
	for i, w := range want {
		if st.Stages[i].Name != w {
			t.Fatalf("阶段顺序不符: index %d got %q want %q（全部阶段: %+v）", i, st.Stages[i].Name, w, st.Stages)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	return string(data)
}

func tail(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > 40 {
		lines = lines[len(lines)-40:]
	}
	return strings.Join(lines, "\n")
}

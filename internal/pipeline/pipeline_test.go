package pipeline

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/auto-deployer/auto-deployer/internal/components"
	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/runstate"
)

// scriptComp 按 params["script"] 里的动作序列执行，记录调用顺序。
type scriptComp struct {
	mu       sync.Mutex
	calls    []string
	sleepFor time.Duration // >0 时睡这么久（测超时/取消）
}

func (s *scriptComp) Run(ctx context.Context, req components.Request) (components.Results, error) {
	script, _ := req.Params["script"].(string)
	s.mu.Lock()
	s.calls = append(s.calls, script)
	s.mu.Unlock()
	if s.sleepFor > 0 && script == "x" { // 只有慢节点脚本 x 睡眠；后置清理节点不受 sleepFor 影响
		select {
		case <-time.After(s.sleepFor):
		case <-ctx.Done():
			return components.Results{}, ctx.Err()
		}
	}
	if script == "fail" {
		return components.Results{}, fmt.Errorf("boom")
	}
	return components.Results{"stdout": "out:" + script}, nil
}

func (s *scriptComp) seq() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.calls...)
}

var testComp *scriptComp

func TestMain(m *testing.M) {
	testComp = &scriptComp{}
	components.Register("test", testComp)
	os.Exit(m.Run())
}

func stage(name, when, script string) config.StageConfig {
	return config.StageConfig{
		Name: name, Type: "test", When: when,
		Params: map[string]any{"script": script},
	}
}

func newPipeline(stages ...config.StageConfig) *config.PipelineConfig {
	return &config.PipelineConfig{Name: "t", Workspace: "/tmp/ws-t", Stages: stages}
}

func TestMainFlowFailFast(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testComp.calls = nil
	pl := newPipeline(stage("a", "", "ok"), stage("b", "", "fail"), stage("c", "", "ok"),
		stage("notify-f", "failure", "notify-fail"), stage("clean", "always", "cleanup"),
		stage("notify-s", "", "notify-ok"))
	res := Run(context.Background(), pl, Options{Trigger: "manual"})
	if res.Status != "failed" || res.FailedStage != "b" {
		t.Fatalf("res=%+v", res)
	}
	want := []string{"ok", "fail", "notify-fail", "cleanup"} // c 被跳过；notify-s 是主流程也不跑（记录的是 script 值）
	if fmt.Sprint(testComp.seq()) != fmt.Sprint(want) {
		t.Fatalf("执行序列 = %v, want %v", testComp.seq(), want)
	}
}

func TestMainFlowSuccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testComp.calls = nil
	pl := newPipeline(stage("a", "", "ok"), stage("ok2", "", "ok"),
		stage("notify-f", "failure", "notify-fail"), stage("clean", "always", "cleanup"))
	res := Run(context.Background(), pl, Options{Trigger: "manual"})
	if res.Status != "success" {
		t.Fatalf("res=%+v", res)
	}
	want := []string{"ok", "ok", "cleanup"} // 成功：failure 节点不跑，always 跑
	if fmt.Sprint(testComp.seq()) != fmt.Sprint(want) {
		t.Fatalf("执行序列 = %v, want %v", testComp.seq(), want)
	}
}

func TestSkip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testComp.calls = nil
	skipped := stage("a", "", "ok")
	skipped.Skip = "${env.skipme}"
	pl := newPipeline(skipped, stage("b", "", "ok"))
	pl.Env = map[string]string{"skipme": "true"}
	res := Run(context.Background(), pl, Options{Trigger: "manual"})
	if res.Status != "success" || len(res.Stages) != 2 || res.Stages[0].State != "skipped" {
		t.Fatalf("res=%+v stages=%+v", res, res.Stages)
	}
	if fmt.Sprint(testComp.seq()) != "[ok]" { // fmt.Sprint 切片以空格分隔
		t.Fatalf("被 skip 的节点不应执行: %v", testComp.seq())
	}
}

func TestUndefinedRefFailsStage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testComp.calls = nil
	s := stage("a", "", "ok")
	s.Params = map[string]any{"script": "${env.typo}"} // 未定义引用
	pl := newPipeline(s, stage("clean", "always", "cleanup"))
	res := Run(context.Background(), pl, Options{Trigger: "manual"})
	if res.Status != "failed" || res.FailedStage != "a" {
		t.Fatalf("未定义引用必须让节点失败: %+v", res)
	}
}

func TestOutputParams(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testComp.calls = nil
	first := stage("a", "", "v1")
	first.Output = map[string]string{"val": "${stdout}"}
	second := stage("b", "", "use")
	second.Params = map[string]any{"script": "got:${output.a.val}"}
	pl := newPipeline(first, second)
	res := Run(context.Background(), pl, Options{Trigger: "manual"})
	if res.Status != "success" {
		t.Fatalf("res=%+v", res)
	}
	// scriptComp 拿到的 script 应是 "got:out:v1"（output 已插值）——通过调用记录断言
	if got := testComp.seq()[1]; got != "got:out:v1" {
		t.Fatalf("output 引用未生效: %q", got)
	}
}

func TestTotalTimeoutStillRunsAlways(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testComp.calls = nil
	testComp.sleepFor = 5 * time.Second
	defer func() { testComp.sleepFor = 0 }()
	pl := newPipeline(stage("slow", "", "x"), stage("clean", "always", "cleanup"))
	pl.Timeout = "300ms"
	start := time.Now()
	res := Run(context.Background(), pl, Options{Trigger: "manual"})
	if res.Status != "failed" || time.Since(start) > 3*time.Second {
		t.Fatalf("总预算耗尽应判 failed: %+v elapsed=%v", res, time.Since(start))
	}
	if fmt.Sprint(testComp.seq()) != fmt.Sprint([]string{"x", "cleanup"}) {
		t.Fatalf("总预算耗尽后 always 清理仍要执行: %v", testComp.seq())
	}
}

func TestStageTimeout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testComp.calls = nil
	testComp.sleepFor = 5 * time.Second
	defer func() { testComp.sleepFor = 0 }()
	s := stage("slow", "", "x")
	s.Timeout = "200ms"
	pl := newPipeline(s, stage("clean", "always", "cleanup"))
	res := Run(context.Background(), pl, Options{Trigger: "manual"})
	if res.Status != "failed" || res.FailedStage != "slow" {
		t.Fatalf("节点超时应判 failed: %+v", res)
	}
	if fmt.Sprint(testComp.seq()) != fmt.Sprint([]string{"x", "cleanup"}) {
		t.Fatalf("节点超时后 always 仍要执行: %v", testComp.seq())
	}
}

func TestCancelRunsAlwaysNotFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testComp.calls = nil
	testComp.sleepFor = 2 * time.Second
	defer func() { testComp.sleepFor = 0 }()
	pl := newPipeline(stage("slow", "", "x"),
		stage("notify-f", "failure", "nf"), stage("clean", "always", "cleanup"))
	// 起跑后 300ms 写取消 sentinel
	go func() {
		time.Sleep(300 * time.Millisecond)
		if err := writeCancelForTest("t"); err != nil {
			t.Error(err)
		}
	}()
	res := Run(context.Background(), pl, Options{Trigger: "manual"})
	if res.Status != "cancelled" {
		t.Fatalf("取消应判 cancelled 而非 failed: %+v", res)
	}
	want := []string{"x", "cleanup"} // failure 节点不跑，always 跑
	if fmt.Sprint(testComp.seq()) != fmt.Sprint(want) {
		t.Fatalf("取消后执行序列 = %v, want %v", testComp.seq(), want)
	}
}

func writeCancelForTest(name string) error { return runstate.WriteCancel(name) }

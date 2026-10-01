// Package pipeline 是工作项执行引擎：按列表顺序跑主流程节点（fail-fast），
// 结束后按 when 条件跑后置节点（failure/always）。
//
// 超时层级：总预算 ctx（默认 30m）罩主流程；节点 timeout 是其子 ctx。
// 后置节点走「父 ctx + 10m 硬上限」——总预算耗尽/节点超时后清理仍要执行。
package pipeline

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/auto-deployer/auto-deployer/internal/components"
	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/logger"
	"github.com/auto-deployer/auto-deployer/internal/notify"
	"github.com/auto-deployer/auto-deployer/internal/param"
	"github.com/auto-deployer/auto-deployer/internal/runstate"
)

type Options struct {
	Trigger string // manual | webhook
	Args    map[string]string
	Pushers []string // webhook 队列合并进来的作者（含最新任务）
	Branch  string   // webhook 命中分支；手动为空（取 TriggerBranches 第一个）
	Commit  string
	Author  string
	Message string
	Console io.Writer // exec 前台终端；nil = 只写日志文件
	Cfg     *config.AppConfig
}

type StageResult struct {
	Name     string
	State    string // success | skipped | failed
	Duration string
}

type Result struct {
	Status      string // success | failed | cancelled
	FailedStage string
	Error       string
	Stages      []StageResult
}

const (
	defaultTimeout  = 30 * time.Minute
	postBudget      = 10 * time.Minute // ponytail: 后置节点硬上限；不够时按节点 timeout 细化
	cancelPollEvery = 500 * time.Millisecond
)

func Run(ctx context.Context, pl *config.PipelineConfig, opts Options) *Result {
	log := logger.GetServiceLogger(pl.Name)
	out := io.Writer(log)
	if opts.Console != nil {
		out = io.MultiWriter(log, opts.Console)
	}
	say := func(format string, a ...any) {
		log.Printf(format, a...)
		if opts.Console != nil {
			fmt.Fprintf(opts.Console, format+"\n", a...)
		}
	}

	branch := opts.Branch
	if branch == "" && len(pl.TriggerBranches) > 0 {
		branch = pl.TriggerBranches[0]
	}

	st := &runstate.RunState{
		State: "running", Trigger: opts.Trigger, PID: os.Getpid(),
		StartedAt: time.Now(), Branch: branch, Commit: opts.Commit,
	}
	_ = runstate.ClearCancel(pl.Name)
	_ = runstate.Save(pl.Name, st)

	say("=== %s %s [%s %s %s] ===", time.Now().Format("2006-01-02 15:04:05"), pl.Name, opts.Trigger, branch, opts.Commit)

	ps := param.New()
	for k, v := range pl.Env {
		ps.Env[k] = v
	}
	for k, v := range opts.Args {
		ps.Args[k] = v
	}
	ps.System = map[string]string{
		"name": pl.Name, "trigger": opts.Trigger, "branch": branch,
		"commit": opts.Commit, "author": opts.Author, "message": opts.Message,
		"pushers": strings.Join(opts.Pushers, ","), "workspace": pl.Workspace,
	}
	// email 组件 to 缺省 = 全局 notifications.to + 本次 pushers（去重前直接拼接即可）
	if opts.Cfg != nil {
		all := append([]string{}, opts.Cfg.Notifications.To...)
		all = append(all, opts.Pushers...)
		ps.System["default_to"] = strings.Join(all, ", ")
	}

	total, err := pl.TimeoutDuration()
	if err != nil || total <= 0 {
		total = defaultTimeout
	}
	mainCtx, cancelAll := context.WithTimeout(ctx, total)
	defer cancelAll()
	go watchCancel(mainCtx, pl.Name, cancelAll, log)
	stopSig := make(chan os.Signal, 1)
	signal.Notify(stopSig, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stopSig)
	go func() { select { case <-stopSig: cancelAll(); case <-mainCtx.Done(): } }()

	notifier := buildNotifier(opts.Cfg)
	res := &Result{}
	mainErr := runFlow(mainCtx, pl, ps, st, res, opts, out, say, notifier, false, "")

	switch {
	case mainCtx.Err() == context.Canceled:
		res.Status = "cancelled"
	case mainErr != nil:
		res.Status = "failed"
	default:
		res.Status = "success"
	}

	// 后置流程：走父 ctx——总预算耗尽后清理仍要执行（Review Focus #2）
	ps.System["result"] = res.Status
	ps.System["failed_stage"] = res.FailedStage
	postCtx, cancelPost := context.WithTimeout(ctx, postBudget)
	defer cancelPost()
	_ = runFlow(postCtx, pl, ps, st, res, opts, out, say, notifier, true, res.Status)

	now := time.Now()
	st.State = res.Status
	st.FailedStage = res.FailedStage
	st.FailureReason = res.Error
	st.FinishedAt = &now
	_ = runstate.Save(pl.Name, st)
	say("=== %s %s 耗时 %s ===", pl.Name, res.Status, time.Since(st.StartedAt).Round(time.Second))
	return res
}

// runFlow 跑一轮节点。isPost=false 只跑 when=="" 的主流程（fail-fast）；
// isPost=true 按 outcome 跑 when==always / failure 的后置节点（失败只记日志）。
func runFlow(ctx context.Context, pl *config.PipelineConfig, ps *param.Set,
	st *runstate.RunState, res *Result, opts Options, out io.Writer, say func(string, ...any),
	notifier *notify.Notifier, isPost bool, outcome string) error {

	for _, stage := range pl.Stages {
		if !isPost && stage.When != "" {
			continue // 主流程只跑 when 缺省节点
		}
		if isPost {
			if stage.When == "always" {
				// 恒跑
			} else if stage.When == "failure" && outcome == "failed" {
				// 失败时跑（cancelled ≠ failed）
			} else {
				continue
			}
		}

		skip, err := ps.SkipTrue(stage.Skip)
		if err != nil {
			if isPost { // 后置节点 pre-run 错误只记日志，不中断后续 always、不覆盖主结果
				say("--- [%s] 后置节点失败（忽略）: skip 求值失败: %v ---", stage.Name, err)
				record(st, res, stage.Name, "failed", "")
				continue
			}
			return failStage(stage, res, st, fmt.Sprintf("skip 求值失败: %v", err))
		}
		if skip {
			record(st, res, stage.Name, "skipped", "")
			say("--- [%s] skipped ---", stage.Name)
			continue
		}

		stageCtx := ctx
		if d, err := stage.TimeoutDuration(); err == nil && d > 0 {
			var cancel context.CancelFunc
			stageCtx, cancel = context.WithTimeout(ctx, d)
			defer cancel() // ponytail: 循环内 defer 会积压到函数尾；节点数少（个位数）可接受
		}

		params, err := ps.InterpolateParams(stage.Params)
		if err != nil {
			if isPost { // Review Focus #1 只约束主流程；后置未定义引用同样只记日志
				say("--- [%s] 后置节点失败（忽略）: %v ---", stage.Name, err)
				record(st, res, stage.Name, "failed", "")
				continue
			}
			return failStage(stage, res, st, err.Error()) // Review Focus #1
		}
		applyDefaults(pl, stage, params, ps)
		comp, err := components.Get(stage.Type)
		if err != nil {
			if isPost {
				say("--- [%s] 后置节点失败（忽略）: %v ---", stage.Name, err)
				record(st, res, stage.Name, "failed", "")
				continue
			}
			return failStage(stage, res, st, err.Error())
		}

		say("--- [%s] %s ---", stage.Name, stage.Type)
		start := time.Now()
		results, err := comp.Run(stageCtx, components.Request{
			Params: params, Out: out, Notifier: notifier,
		})
		if err == nil {
			for k, raw := range stage.Output {
				v, oerr := ps.Interpolate(raw, results) // 裸名（如 ${stdout}）在这里生效
				if oerr != nil {
					err = fmt.Errorf("output.%s 求值失败: %w", k, oerr)
					break
				}
				ps.AddOutput(stage.Name, k, v)
			}
		}
		dur := time.Since(start).Round(time.Millisecond)

		if err != nil {
			if isPost {
				// 后置节点失败只记日志，不改变最终状态、不递归
				say("--- [%s] 后置节点失败（忽略）: %v ---", stage.Name, err)
				record(st, res, stage.Name, "failed", dur.String())
				continue
			}
			return failStage(stage, res, st, err.Error())
		}
		record(st, res, stage.Name, "success", dur.String())
		say("--- [%s] 完成 %s ---", stage.Name, dur)
	}
	return nil
}

// applyDefaults 注入组件缺省值（插值后、执行前）：
// shell.cwd / git.workspace / cleanup.workspace → 工作项 workspace；
// git.branch 列表 → 具体分支（system.branch 命中列表用之，否则第一个）；
// email.to → 全局 notifications.to + pushers（Run 预拼进 system.default_to）。
func applyDefaults(pl *config.PipelineConfig, stage config.StageConfig, params map[string]any, ps *param.Set) {
	switch stage.Type {
	case "shell":
		if _, ok := params["cwd"]; !ok {
			params["cwd"] = pl.Workspace
		}
	case "git":
		if _, ok := params["workspace"]; !ok {
			params["workspace"] = pl.Workspace
		}
		if list, ok := params["branch"].([]any); ok {
			concrete := firstOr(ps.System["branch"], list)
			params["branch"] = concrete
		}
	case "cleanup":
		if _, ok := params["workspace"]; !ok {
			params["workspace"] = pl.Workspace
		}
	case "email":
		if _, ok := params["to"]; !ok {
			params["to"] = ps.System["default_to"]
		}
	}
}

func firstOr(concrete string, list []any) string {
	for _, b := range list {
		if s, _ := b.(string); s == concrete && concrete != "" {
			return concrete
		}
	}
	if len(list) > 0 {
		if s, ok := list[0].(string); ok {
			return s
		}
	}
	return concrete
}

func record(st *runstate.RunState, res *Result, name, state, dur string) {
	st.Stages = append(st.Stages, runstate.StageState{Name: name, State: state, Duration: dur})
	res.Stages = append(res.Stages, StageResult{Name: name, State: state, Duration: dur})
}

func failStage(stage config.StageConfig, res *Result, st *runstate.RunState, reason string) error {
	res.FailedStage = stage.Name
	res.Error = reason
	record(st, res, stage.Name, "failed", "")
	return fmt.Errorf("%s: %s", stage.Name, reason)
}

// watchCancel 轮询取消 sentinel，命中即 cancel（沿用旧 deploy 的机制与节奏）。
func watchCancel(ctx context.Context, name string, cancel context.CancelFunc, log *logger.Logger) {
	ticker := time.NewTicker(cancelPollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if runstate.HasCancel(name) {
				log.Printf("收到取消信号，中止 %s", name)
				_ = runstate.ClearCancel(name)
				cancel()
				return
			}
		}
	}
}

func buildNotifier(cfg *config.AppConfig) *notify.Notifier {
	if cfg == nil {
		return nil
	}
	hasSMTP := cfg.SMTP.Host != ""
	hasResend := cfg.Resend.APIKey != ""
	if !hasSMTP && !hasResend {
		return nil
	}
	return notify.New(cfg.SMTP.Host, cfg.SMTP.Port, cfg.SMTP.Username, cfg.SMTP.Token,
		cfg.SMTP.TLS, cfg.Resend.APIKey, cfg.Resend.From)
}

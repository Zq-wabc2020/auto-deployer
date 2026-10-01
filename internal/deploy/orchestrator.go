package deploy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/auto-deployer/auto-deployer/internal/build"
	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deploylock"
	"github.com/auto-deployer/auto-deployer/internal/logger"
	"github.com/auto-deployer/auto-deployer/internal/servstate"
	"github.com/auto-deployer/auto-deployer/internal/term"
)

// Deployer is the universal lifecycle contract every deployment model implements.
// Build produces the artifact; Stage is deploy-only preparation (artifact placement,
// migrate, etc.) that must NOT run on restart; Status reports current state.
// Start/Stop are optional capabilities (see Startable/Stoppable) for models that
// manage a long-running process.
type Deployer interface {
	Build(ctx context.Context, svc *config.ServiceConfig) error
	Stage(ctx context.Context, svc *config.ServiceConfig) error
	Status(ctx context.Context, svc *config.ServiceConfig) (string, error)
	// SetOutput redirects stdout/stderr to the given writer (typically a service log file)
	SetOutput(w io.Writer)
}

// Startable is implemented by models that launch a long-running process
// (jvm/node/python/docker). Models without a per-service process (e.g. static)
// do not implement this; `service start` then returns a clear error.
type Startable interface {
	Start(ctx context.Context, svc *config.ServiceConfig) error
}

// Stoppable is implemented by models that can stop a managed process.
type Stoppable interface {
	Stop(ctx context.Context, svc *config.ServiceConfig) error
}

// DeployResult contains the result of a deployment operation.
type DeployResult struct {
	ServiceName string
	Status      string // "success" | "failed"
	AuthorEmail string
	Error       string
}

// defaultDeployTimeout is the total budget for fetch + build + stage when
// timeout is not configured. A hung git/mvn would otherwise hold the deploy
// lock forever.
const defaultDeployTimeout = 30 * time.Minute

// deployTimeout returns the configured service-level timeout (a single total
// budget shared by fetch + build + stage), or the default. An invalid value
// was already rejected by config validation; fall back to the default rather
// than failing the deploy here.
func deployTimeout(svc *config.ServiceConfig) time.Duration {
	if d, err := svc.TimeoutDuration(); err == nil && d > 0 {
		return d
	}
	return defaultDeployTimeout
}

// withDeployCtx layers a manual cancel on top of the deploy timeout budget:
// WithCancel(WithTimeout(parent, timeout)). The returned CancelFunc cancels
// both, so either the deadline (fetch+build+stage+readiness 超时) or a manual
// cancel (T8 的取消 sentinel) aborts the whole pipeline. ServiceStart (T7) 复用。
func withDeployCtx(parent context.Context, svc *config.ServiceConfig) (context.Context, context.CancelFunc) {
	deadlineCtx, cancelDeadline := context.WithTimeout(parent, deployTimeout(svc))
	deployCtx, manualCancel := context.WithCancel(deadlineCtx)
	return deployCtx, func() {
		manualCancel()
		cancelDeadline()
	}
}

// Deploy executes the full deployment pipeline:
// fetch -> getAuthorEmail -> Build -> Stage -> Stop? -> Start? -> readinessGate? -> notify
//
// 整条 fetch/build/stage/Start/readiness 链跑在 deployCtx 下（WithCancel(WithTimeout)）：
// 既受总超时预算约束，又可被手动取消层中断（T8 取消 sentinel）。失败/取消经 handleErr
// 统一处置；成功则清 .state 并发 success 邮件。
func Deploy(ctx context.Context, svc *config.ServiceConfig, cfg *config.AppConfig, deployer Deployer, operatorEmails []string) (*DeployResult, error) {
	result := &DeployResult{ServiceName: svc.Name}

	// All deploy pipeline output goes to the service log file so that both
	// manual and webhook triggers share the same per-service log destination.
	log := logger.GetServiceLogger(svc.Name)
	deployer.SetOutput(log)

	// Notification recipients: operators who triggered this deploy (merged by
	// the queue for webhooks). For a direct manual trigger (none provided) the
	// fetched commit author is used as the recipient (set after fetch below).
	recipients := operatorEmails
	// 先声明：fetch 后才赋值（fetch 失败时留空）。
	// TODO(Task 10): commitInfo 随旧邮件通知删除；新引擎按需重新引入。
	var authorEmail string

	// 1. Ensure SSH key (pre-deploy setup; 不在 deployCtx 超时预算内)。
	keyFile, _, _, err := build.EnsureSSHKey()
	if err != nil {
		result.Status = "failed"
		result.Error = fmt.Sprintf("failed to ensure SSH key: %v", err)
		log.Printf("deploy failed: %v", err)
		return result, fmt.Errorf(result.Error)
	}

	// ctx 重构：timeout 总预算（fetch+build+stage+就绪）+ 手动取消层（T8 sentinel）。
	deployCtx, cancelDeploy := withDeployCtx(ctx, svc)
	defer cancelDeploy()

	// 清残留取消 sentinel（T8 接线 watcher；此处仅清旧文件，不阻塞）。
	_ = servstate.ClearCancel(svc.Name)
	// T8：watchCancel 轮询取消 sentinel，命中则 cancel deployCtx；SIGINT 等价取消。
	go watchCancel(deployCtx, svc.Name, cancelDeploy, log)
	stopSig := make(chan os.Signal, 1)
	signal.Notify(stopSig, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stopSig)
	go func() { select { case <-stopSig: cancelDeploy(); case <-deployCtx.Done(): } }()

	// 写 starting 状态（每阶段刷新子阶段，供 status 查询展示当前进度）。
	setStage := func(s string) { _ = servstate.WriteStarting(svc.Name, s) }
	setStage("fetch")

	// started 标记 Start 是否已执行，供 handleErr 决定是否停半起服务。
	started := false

	// handleErr 统一处置失败/取消：区分手动取消（Canceled）与其余（含 DeadlineExceeded 超时）。
	handleErr := func(stage string, e error) (*DeployResult, error) {
		if deployCtx.Err() == context.Canceled {
			// 手动取消：不发邮件，停半起服务，清 state。
			if started {
				if s, ok := deployer.(Stoppable); ok {
					_ = s.Stop(ctx, svc)
				}
			}
			_ = servstate.Clear(svc.Name)
			result.Status = "cancelled"
			log.Printf("deploy cancelled at %s", stage)
			return result, fmt.Errorf("deploy cancelled: %s", stage)
		}
		// 失败（含超时）：停半起服务 + start_failed + 邮件。
		if started {
			if s, ok := deployer.(Stoppable); ok {
				_ = s.Stop(ctx, svc)
			}
		}
		_ = servstate.WriteFailed(svc.Name)
		result.Status = "failed"
		result.Error = e.Error()
		// TODO(Task 10): sendNotify(ctx, cfg, svc, log, recipients, authorEmail, commitInfo, stage, "failed", e.Error())
		return result, e
	}

	log.Printf("fetching %s to %s...", svc.Repo.URL, svc.Workspace)
	fetchStart := time.Now()
	if err := build.Fetch(deployCtx, svc.Repo.URL, keyFile, svc.Repo.Branch, svc.Workspace, log); err != nil {
		return handleErr("fetch", err)
	}
	log.Printf("fetch done in %s", time.Since(fetchStart).Round(time.Millisecond))

	// 2. Get author email from latest commit (used for the notification body and
	// as the recipient fallback for direct manual triggers).
	authorEmail = build.GetLatestAuthorEmail(svc.Workspace, svc.Repo.Branch)
	if len(recipients) == 0 {
		recipients = []string{authorEmail}
	}

	// 3. Build
	setStage("build")
	log.Printf("building %s...", svc.Name)
	buildStart := time.Now()
	if err := deployer.Build(deployCtx, svc); err != nil {
		return handleErr("build", err)
	}
	log.Printf("build done in %s", time.Since(buildStart).Round(time.Millisecond))

	// 4. Stage (deploy-only preparation: artifact placement, migrate, etc.).
	// Skipped on restart -- this is what keeps Start a pure launch.
	setStage("stage")
	log.Printf("staging %s...", svc.Name)
	stageStart := time.Now()
	if err := deployer.Stage(deployCtx, svc); err != nil {
		return handleErr("stage", err)
	}
	log.Printf("stage done in %s", time.Since(stageStart).Round(time.Millisecond))

	// 5. Stop old instance (only if the model manages a process). Stop 走 parent
	// ctx：即便 deployCtx 已超时，仍需能清理旧进程。
	if s, ok := deployer.(Stoppable); ok {
		log.Printf("stopping %s...", svc.Name)
		_ = s.Stop(ctx, svc)
	}

	// 6. Start new instance + 就绪门控（only if the model runs a process）。
	if s, ok := deployer.(Startable); ok {
		log.Printf("starting %s...", svc.Name)
		setStage("start")
		if err := s.Start(deployCtx, svc); err != nil {
			return handleErr("start", err)
		}
		started = true
		setStage("readiness")
		if err := readinessGate(deployCtx, svc, deployer, log); err != nil {
			return handleErr("readiness", err)
		}
	}

	// 7. Success：清 starting 状态，发 success 邮件。
	_ = servstate.Clear(svc.Name)
	result.Status = "success"
	result.AuthorEmail = authorEmail
	// TODO(Task 10): sendNotify(ctx, cfg, svc, log, recipients, authorEmail, commitInfo, "", "success", "")
	log.Printf("%s deployed successfully", svc.Name)
	return result, nil
}

// readinessGate 在 deployCtx 下轮询 svc.HealthURL 直至 2xx/3xx 通过；每轮先做
// 进程存活快速失败（Stoppable 模型 Status==stopped 即报错，static 无进程跳过）；
// health 未过则等 ticker 下一轮或 ctx.Done（区分 Canceled/DeadlineExceeded）。
// 非忙轮询：ticker + select。
func readinessGate(ctx context.Context, svc *config.ServiceConfig, deployer Deployer, log *logger.Logger) error {
	interval, _ := svc.HealthIntervalDuration()
	client := &http.Client{Timeout: 3 * time.Second}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		// 进程/容器存活快速失败（static 无进程，跳过）。
		if _, ok := deployer.(Stoppable); ok {
			if st, _ := deployer.Status(ctx, svc); st == "stopped" {
				return fmt.Errorf("服务进程启动后立即退出（%s）", svc.Name)
			}
		}
		// 用 ctx 构造请求：取消可即时中断在飞的健康探测，不必等 3s client.Timeout。
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, svc.HealthURL, nil)
		if err == nil {
			resp, err := client.Do(req)
			if err == nil {
				if resp.StatusCode >= 200 && resp.StatusCode < 400 {
					resp.Body.Close()
					log.Printf("health check passed: %s", svc.HealthURL)
					return nil
				}
				resp.Body.Close()
			}
			// err != nil 或非 2xx/3xx → 视为未就绪，进入 select 等待
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err() // Canceled 或 DeadlineExceeded
		}
	}
}

// ServiceStart starts a service without rebuilding. 走 starting → Start → 就绪门控：
// 用 withDeployCtx 取总预算+手动取消层；先清残留取消 sentinel（T8 接线 watcher，此处仅清旧），
// 写 starting，Start 后跑 readinessGate。成功清 .state；失败/取消区分手动取消（Stop 半起 + 清 state，
// 不发邮件）与超时/真失败（Stop + start_failed）。
func ServiceStart(ctx context.Context, svc *config.ServiceConfig, deployer Deployer) error {
	s, ok := deployer.(Startable)
	if !ok {
		return fmt.Errorf("%s 服务类型不支持 start，请用 deploy 重新发布", svc.Type)
	}
	deployCtx, cancelDeploy := withDeployCtx(ctx, svc)
	defer cancelDeploy()
	log := logger.GetServiceLogger(svc.Name)
	// 清残留取消 sentinel（T8 watcher 接线；此处仅清旧文件，不阻塞）。
	_ = servstate.ClearCancel(svc.Name)
	// T8：watchCancel 轮询取消 sentinel，命中则 cancel deployCtx。
	go watchCancel(deployCtx, svc.Name, cancelDeploy, log)
	// SIGINT/SIGTERM（前台进程）等价取消；forked Linux 子进程靠 sentinel。
	stopSig := make(chan os.Signal, 1)
	signal.Notify(stopSig, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stopSig)
	go func() { select { case <-stopSig: cancelDeploy(); case <-deployCtx.Done(): } }()

	_ = servstate.WriteStarting(svc.Name, "start")
	if err := s.Start(deployCtx, svc); err != nil {
		if deployCtx.Err() == context.Canceled {
			// 手动取消：清 state，不发邮件。
			_ = servstate.Clear(svc.Name)
			return ctxErr(deployCtx)
		}
		_ = servstate.WriteFailed(svc.Name)
		return err
	}
	if err := readinessGate(deployCtx, svc, deployer, log); err != nil {
		if deployCtx.Err() == context.Canceled {
			// 手动取消：Stop 半起 + 清 state，不发邮件。
			if st, ok := deployer.(Stoppable); ok {
				_ = st.Stop(ctx, svc)
			}
			_ = servstate.Clear(svc.Name)
			return ctxErr(deployCtx)
		}
		// 失败/超时：Stop 半起 + start_failed。
		if st, ok := deployer.(Stoppable); ok {
			_ = st.Stop(ctx, svc)
		}
		_ = servstate.WriteFailed(svc.Name)
		return err
	}
	_ = servstate.Clear(svc.Name)
	return nil
}

// watchCancel 轮询 <name>.cancel sentinel，命中则调 cancel() 并清 sentinel。
// 随 ctx 退出（select 听 ctx），不泄漏。
func watchCancel(ctx context.Context, name string, cancel context.CancelFunc, log *logger.Logger) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if servstate.HasCancel(name) {
				log.Printf("收到取消信号，中止 %s", name)
				_ = servstate.ClearCancel(name)
				cancel()
				return
			}
		}
	}
}

// ctxErr 把 ctx.Err() 包装为 "cancelled: %w"；ctx 未取消时返回 nil。
func ctxErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("cancelled: %w", err)
	}
	return nil
}

// ServiceStop stops a service and clears its persistent state (start_failed → stopped)。
func ServiceStop(ctx context.Context, svc *config.ServiceConfig, deployer Deployer) error {
	if s, ok := deployer.(Stoppable); ok {
		if err := s.Stop(ctx, svc); err != nil {
			return err
		}
	}
	// stop 清除 start_failed → stopped（无持久态，回到实时探测）。
	_ = servstate.Clear(svc.Name)
	return nil
}

// ServiceRestart stops and starts a service without rebuilding (no Build/Stage)。
// ServiceStart 内部已含 starting + 就绪门控 + 失败处置。
func ServiceRestart(ctx context.Context, svc *config.ServiceConfig, deployer Deployer) error {
	if _, ok := deployer.(Startable); !ok {
		return fmt.Errorf("%s 服务类型不支持 restart，请用 deploy 重新发布", svc.Type)
	}
	if s, ok := deployer.(Stoppable); ok {
		_ = s.Stop(ctx, svc)
	}
	return ServiceStart(ctx, svc, deployer)
}

// GetServiceStatus returns the status of a service.
func GetServiceStatus(ctx context.Context, svc *config.ServiceConfig, deployer Deployer) (string, error) {
	return deployer.Status(ctx, svc)
}

// GetServiceStatusRich 按优先级返回（已上色）服务状态：
//  1. .state=starting 且锁持有 → starting（部署仍在进行）
//  2. .state=starting 且锁空 → 陈旧，就地重写为 start_failed
//  3. .state=start_failed → start_failed（粘性，直到下次 deploy 成功清除）
//  4. 无 .state → 插件实时探测
//
// 陈旧恢复（第 2 步）由读取方（status 查询）执行写操作，这是“只有部署进程写状态”
// 的唯一例外：锁空意味着无部署在跑，不存在并发写入方，因此就地重写安全。
func GetServiceStatusRich(ctx context.Context, svc *config.ServiceConfig, deployer Deployer) (string, error) {
	st, ok := servstate.Read(svc.Name)
	if ok && st.Status == "starting" {
		if deploylock.IsHeld(svc.Name) {
			return term.Colorize("starting"), nil
		}
		// 陈旧：部署进程已死，就地重写为失败
		_ = servstate.WriteFailed(svc.Name)
		return term.Colorize("start_failed"), nil
	}
	if ok && st.Status == "start_failed" {
		return term.Colorize("start_failed"), nil
	}
	// 无持久态 → 实时探测
	raw, err := deployer.Status(ctx, svc)
	if err != nil {
		return term.Colorize("unknown"), err
	}
	return term.Colorize(raw), nil
}

// TODO(Task 10): sendNotify/buildNotifier（旧编排的邮件通知）已随
// notify.Send 按次传收件人的改造删除；新引擎的 email 组件接管通知。

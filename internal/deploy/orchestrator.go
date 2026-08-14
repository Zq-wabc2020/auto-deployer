package deploy

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/auto-deployer/auto-deployer/internal/build"
	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/logger"
	"github.com/auto-deployer/auto-deployer/internal/notify"
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

// Deploy executes the full deployment pipeline:
// fetch -> getAuthorEmail -> plugin.Build -> plugin.Stage -> plugin.Stop? -> plugin.Start? -> notify
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
	commitInfo := ""

	// 1. Fetch fresh code
	keyFile, _, _, err := build.EnsureSSHKey()
	if err != nil {
		result.Status = "failed"
		result.Error = fmt.Sprintf("failed to ensure SSH key: %v", err)
		log.Printf("deploy failed: %v", err)
		return result, fmt.Errorf(result.Error)
	}

	log.Printf("fetching %s to %s...", svc.Repo.URL, svc.Workspace)
	if err := build.Fetch(svc.Repo.URL, keyFile, svc.Repo.Branch, svc.Workspace, log); err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		log.Printf("fetch failed: %v", err)
		sendNotify(ctx, cfg, svc, log, recipients, "", commitInfo, "failed", err.Error())
		return result, err
	}

	// 2. Get author email from latest commit (used for the notification body and
	// as the recipient fallback for direct manual triggers).
	authorEmail := build.GetLatestAuthorEmail(svc.Workspace, svc.Repo.Branch)
	commitInfo = build.GetLatestCommit(svc.Workspace, svc.Repo.Branch)
	if len(recipients) == 0 {
		recipients = []string{authorEmail}
	}

	// 3. Build
	log.Printf("building %s...", svc.Name)
	if err := deployer.Build(ctx, svc); err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		log.Printf("build failed: %v", err)
		sendNotify(ctx, cfg, svc, log, recipients, authorEmail, commitInfo, "failed", err.Error())
		return result, err
	}

	// 4. Stage (deploy-only preparation: artifact placement, migrate, etc.).
	// Skipped on restart -- this is what keeps Start a pure launch.
	log.Printf("staging %s...", svc.Name)
	if err := deployer.Stage(ctx, svc); err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		log.Printf("stage failed: %v", err)
		sendNotify(ctx, cfg, svc, log, recipients, authorEmail, commitInfo, "failed", err.Error())
		return result, err
	}

	// 5. Stop old instance (only if the model manages a process)
	if s, ok := deployer.(Stoppable); ok {
		log.Printf("stopping %s...", svc.Name)
		_ = s.Stop(ctx, svc)
	}

	// 6. Start new instance (only if the model runs a process)
	if s, ok := deployer.(Startable); ok {
		log.Printf("starting %s...", svc.Name)
		if err := s.Start(ctx, svc); err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			log.Printf("start failed: %v", err)
			sendNotify(ctx, cfg, svc, log, recipients, authorEmail, commitInfo, "failed", err.Error())
			return result, err
		}
	}

	result.Status = "success"
	result.AuthorEmail = authorEmail
	sendNotify(ctx, cfg, svc, log, recipients, authorEmail, commitInfo, "success", "")
	log.Printf("%s deployed successfully", svc.Name)
	return result, nil
}

// ServiceStart starts a service without rebuilding.
func ServiceStart(ctx context.Context, svc *config.ServiceConfig, deployer Deployer) error {
	if s, ok := deployer.(Startable); ok {
		return s.Start(ctx, svc)
	}
	return fmt.Errorf("%s 服务类型不支持 start，请用 deploy 重新发布", svc.Type)
}

// ServiceStop stops a service.
func ServiceStop(ctx context.Context, svc *config.ServiceConfig, deployer Deployer) error {
	if s, ok := deployer.(Stoppable); ok {
		return s.Stop(ctx, svc)
	}
	return fmt.Errorf("%s 服务类型不支持 stop，请用 deploy 重新发布", svc.Type)
}

// ServiceRestart stops and starts a service without rebuilding (no Build/Stage).
func ServiceRestart(ctx context.Context, svc *config.ServiceConfig, deployer Deployer) error {
	if _, ok := deployer.(Startable); !ok {
		return fmt.Errorf("%s 服务类型不支持 restart，请用 deploy 重新发布", svc.Type)
	}
	if s, ok := deployer.(Stoppable); ok {
		_ = s.Stop(ctx, svc)
	}
	return deployer.(Startable).Start(ctx, svc)
}

// GetServiceStatus returns the status of a service.
func GetServiceStatus(ctx context.Context, svc *config.ServiceConfig, deployer Deployer) (string, error) {
	return deployer.Status(ctx, svc)
}

func sendNotify(ctx context.Context, cfg *config.AppConfig, svc *config.ServiceConfig, log *logger.Logger, recipients []string, authorEmail, commitInfo, status, errMsg string) {
	if notifier := buildNotifier(cfg, recipients); notifier != nil {
		to := strings.Join(recipients, ", ")
		if to == "" {
			to = "(configured subscribers only)"
		}
		log.Printf("sending notification to: %s", to)
		notice := notify.DeployNotice{
			ServiceName: svc.Name,
			Branch:      svc.Repo.Branch,
			CommitInfo:  commitInfo,
			AuthorEmail: authorEmail,
			Status:      status,
			ErrMsg:      errMsg,
		}
		if err := notifier.NotifyDeployResult(ctx, notice); err != nil {
			log.Printf("warning: failed to send notification: %v", err)
		} else {
			log.Printf("notification sent successfully")
		}
	} else {
		log.Printf("no notifier configured (SMTP/Resend not set)")
	}
}

// buildNotifier creates a Notifier from config. Recipients are the deploy
// operators (merged by the queue); configured notifications.to are appended.
func buildNotifier(cfg *config.AppConfig, recipients []string) *notify.Notifier {
	hasSMTP := cfg != nil && cfg.SMTP.Host != ""
	hasResend := cfg != nil && cfg.Resend.APIKey != ""
	if !hasSMTP && !hasResend {
		return nil
	}
	all := append([]string{}, recipients...)
	all = append(all, cfg.Notifications.To...)
	return notify.New(
		cfg.SMTP.Host,
		cfg.SMTP.Port,
		cfg.SMTP.Username,
		cfg.SMTP.Token,
		cfg.SMTP.TLS,
		cfg.Resend.APIKey,
		cfg.Resend.From,
		all,
	)
}

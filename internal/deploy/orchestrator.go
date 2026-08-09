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

// Deployer handles the build and deploy logic for a service type.
type Deployer interface {
	Build(ctx context.Context, svc *config.ServiceConfig) error
	Start(ctx context.Context, svc *config.ServiceConfig) error
	Stop(ctx context.Context, svc *config.ServiceConfig) error
	Status(ctx context.Context, svc *config.ServiceConfig) (string, error)
	// SetOutput redirects stdout/stderr to the given writer (typically a service log file)
	SetOutput(w io.Writer)
}

// DeployResult contains the result of a deployment operation.
type DeployResult struct {
	ServiceName string
	Status      string // "success" | "failed"
	AuthorEmail string
	Error       string
}

// Deploy executes the full deployment pipeline:
// fetch → getAuthorEmail → plugin.Build → plugin.Stop → plugin.Start → notify
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
		sendNotify(ctx, cfg, svc, log, recipients, "", "failed", err.Error())
		return result, err
	}

	// 2. Get author email from latest commit (used for the notification body and
	// as the recipient fallback for direct manual triggers).
	authorEmail := build.GetLatestAuthorEmail(svc.Workspace, svc.Repo.Branch)
	if len(recipients) == 0 {
		recipients = []string{authorEmail}
	}

	// 3. Build
	log.Printf("building %s...", svc.Name)
	if err := deployer.Build(ctx, svc); err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		log.Printf("build failed: %v", err)
		sendNotify(ctx, cfg, svc, log, recipients, authorEmail, "failed", err.Error())
		return result, err
	}

	// 4. Stop old instance
	log.Printf("stopping %s...", svc.Name)
	_ = deployer.Stop(ctx, svc)

	// 5. Start new instance
	log.Printf("starting %s...", svc.Name)
	if err := deployer.Start(ctx, svc); err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		log.Printf("start failed: %v", err)
		sendNotify(ctx, cfg, svc, log, recipients, authorEmail, "failed", err.Error())
		return result, err
	}

	result.Status = "success"
	result.AuthorEmail = authorEmail
	sendNotify(ctx, cfg, svc, log, recipients, authorEmail, "success", "")
	log.Printf("%s deployed successfully", svc.Name)
	return result, nil
}

// ServiceStart starts a service without rebuilding.
func ServiceStart(ctx context.Context, svc *config.ServiceConfig, deployer Deployer) error {
	return deployer.Start(ctx, svc)
}

// ServiceStop stops a service.
func ServiceStop(ctx context.Context, svc *config.ServiceConfig, deployer Deployer) error {
	return deployer.Stop(ctx, svc)
}

// ServiceRestart stops and starts a service without rebuilding.
func ServiceRestart(ctx context.Context, svc *config.ServiceConfig, deployer Deployer) error {
	_ = deployer.Stop(ctx, svc)
	return deployer.Start(ctx, svc)
}

// GetServiceStatus returns the status of a service.
func GetServiceStatus(ctx context.Context, svc *config.ServiceConfig, deployer Deployer) (string, error) {
	return deployer.Status(ctx, svc)
}

func sendNotify(ctx context.Context, cfg *config.AppConfig, svc *config.ServiceConfig, log *logger.Logger, recipients []string, authorEmail, status, errMsg string) {
	if notifier := buildNotifier(cfg, recipients); notifier != nil {
		to := strings.Join(recipients, ", ")
		if to == "" {
			to = "(configured subscribers only)"
		}
		log.Printf("sending notification to: %s", to)
		if err := notifier.NotifyDeployResult(ctx, svc.Name, svc.Repo.Branch, authorEmail, status, errMsg); err != nil {
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

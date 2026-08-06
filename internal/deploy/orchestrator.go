package deploy

import (
	"context"
	"fmt"
	"io"

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
func Deploy(ctx context.Context, svc *config.ServiceConfig, cfg *config.AppConfig, deployer Deployer) (*DeployResult, error) {
	result := &DeployResult{ServiceName: svc.Name}
	log := logger.GetServiceLogger(svc.Name)

	// Redirect plugin output to service log file
	deployer.SetOutput(log)

	// 1. Fetch fresh code
	keyFile, _, _, err := build.EnsureSSHKey()
	if err != nil {
		result.Status = "failed"
		result.Error = fmt.Sprintf("failed to ensure SSH key: %v", err)
		return result, fmt.Errorf(result.Error)
	}

	log.Printf("fetching %s to %s...", svc.Repo.URL, svc.Workspace)
	if err := build.Fetch(svc.Repo.URL, keyFile, svc.Repo.Branch, svc.Workspace); err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		sendNotify(ctx, cfg, svc, "", "failed", err.Error())
		return result, err
	}

	// 2. Get author email from latest commit
	authorEmail := build.GetLatestAuthorEmail(svc.Workspace, svc.Repo.Branch)

	// 3. Build
	log.Printf("building %s...", svc.Name)
	if err := deployer.Build(ctx, svc); err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		sendNotify(ctx, cfg, svc, authorEmail, "failed", err.Error())
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
		sendNotify(ctx, cfg, svc, authorEmail, "failed", err.Error())
		return result, err
	}

	result.Status = "success"
	result.AuthorEmail = authorEmail
	sendNotify(ctx, cfg, svc, authorEmail, "success", "")
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

func sendNotify(ctx context.Context, cfg *config.AppConfig, svc *config.ServiceConfig, authorEmail, status, errMsg string) {
	log := logger.GetServiceLogger(svc.Name)
	if notifier := buildNotifier(cfg, authorEmail); notifier != nil {
		log.Printf("sending notification to: %s", authorEmail)
		if err := notifier.NotifyDeployResult(ctx, svc.Name, svc.Repo.Branch, authorEmail, status, errMsg); err != nil {
			log.Printf("warning: failed to send notification: %v", err)
		} else {
			log.Printf("notification sent successfully")
		}
	} else {
		log.Printf("no notifier configured (SMTP/Resend not set)")
	}
}

// buildNotifier creates a Notifier from config.
func buildNotifier(cfg *config.AppConfig, authorEmail string) *notify.Notifier {
	hasSMTP := cfg != nil && cfg.SMTP.Host != ""
	hasResend := cfg != nil && cfg.Resend.APIKey != ""
	if !hasSMTP && !hasResend {
		return nil
	}
	recipients := []string{authorEmail}
	recipients = append(recipients, cfg.Notifications.To...)
	return notify.New(
		cfg.SMTP.Host,
		cfg.SMTP.Port,
		cfg.SMTP.Username,
		cfg.SMTP.Token,
		cfg.SMTP.TLS,
		cfg.Resend.APIKey,
		cfg.Resend.From,
		recipients,
	)
}

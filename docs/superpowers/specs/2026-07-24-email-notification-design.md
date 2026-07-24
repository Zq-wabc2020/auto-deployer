# Email Notification Design

**Date:** 2026-07-24
**Status:** Approved

## Overview

Add email notification support to deployd. On deployment failure, send an immediate email with full error details for troubleshooting. On successful completion of the entire deployment flow, send one confirmation email. Recipients include the git author's email (extracted from webhook payload) plus a configured list of notification recipients.

## Configuration

Add two top-level blocks to `config.yaml`:

```yaml
smtp:
  host: "smtp.qq.com"
  port: 465
  username: "xxx@qq.com"
  token: "授权码"   # SMTP authorization code, not login password
  tls: true        # STARTTLS or SSL

notifications:
  to:
    - "admin@example.com"
    - "team@example.com"
```

Validation rule: when `notifications.to` is non-empty, all `smtp.*` fields become required.

## Architecture

### New package: `internal/notify`

Single file `email.go`:

| Component | Responsibility |
|-----------|---------------|
| `SMTPConfig` struct | Holds host, port, username, token, tls flag |
| `NotificationConfig` struct | Holds to-list |
| `Notifier` struct | Wraps SMTP config + to-list, holds a single `*smtp.Client` or TLS connection |
| `New(cfg SMTPConfig, to []string) (*Notifier, error)` | Constructor, validates config |
| `Send(ctx, subject, body string) error` | Low-level send, accepts HTML body |
| `NotifyDeployResult(ctx, svcName, branch, authorEmail, status, errMsg string) error` | High-level: assembles subject/body, sends to all recipients |

SMTP dial uses `crypto/tls.Config` for port 465 (SSL), or `STARTTLS` for other ports. Auth uses `smtp.PlainAuth` with username + token.

### Recipients

- All addresses in `notifications.to`
- `authorEmail` extracted from webhook payload (appended if non-empty)
- BCC the daemon itself is not needed; all recipients see each other (acceptable for team notifications)

### Email content

**Subject format:**
- Success: `[deployd] ✅ 部署成功: <service-name>`
- Failure: `[deployd] ❌ 部署失败: <service-name>`

**Body (HTML table format):**
- Deployment success: service name, branch, author, timestamp, build command, run command
- Deployment failure: all above + failure stage (git pull / build / restart), full error message, timestamp

### Webhook payload extension

Extend `DispatchResult` in `internal/webhook/server.go`:

```go
type DispatchResult struct {
    ServiceName  string
    Branch       string
    RepoURL      string
    AuthorEmail  string   // new field
}
```

Extract from:
- **GitHub**: `commits[0].author.email` (falls back to `committer.email`, then empty)
- **Gitee**: same field path (`commits[0].author.email`)
- If commits array is empty or email is missing, `AuthorEmail` stays empty — notification still sends to configured `to` list only

### Integration points

1. **Webhook handler** (`internal/webhook/server.go`): After matching a service, call `NotifyDeployResult("running", "")` to fire off a "deployment started" log entry (no email yet). The actual success/failure email fires after the deploy flow completes.

2. **TriggerDeploy command** (`internal/daemon/commands.go`): Replace the TODO deploy flow stub with real execution, calling `NotifyDeployResult` on success or failure at each step.

3. **Config wizard** (`internal/config/wizard.go`): Add prompts for SMTP host/port/username/token and notification to-list during interactive setup.

## Files changed

| Action | File |
|--------|------|
| Modify | `internal/config/config.go` — add SMTPConfig, NotificationConfig |
| Modify | `internal/config/validate.go` — add SMTP field validation |
| Modify | `internal/config/wizard.go` — add SMTP/notification prompts |
| Modify | `internal/webhook/server.go` — add AuthorEmail to DispatchResult, extract from payload |
| Modify | `internal/daemon/commands.go` — integrate notifier into TriggerDeploy |
| Add | `internal/notify/email.go` — core email sending logic |
| Add | `internal/notify/email_test.go` — unit tests |
| Modify | `config.yaml.example` — add SMTP/notification examples |

## Dependencies

Standard library only: `crypto/tls`, `net/smtp`, `time`. No third-party packages needed.

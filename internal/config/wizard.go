package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// RunWizard runs an interactive wizard that prompts the user for configuration
// values and writes a YAML config file at configPath.
func RunWizard(w io.Writer, r io.Reader, configPath string) error {
	scanner := bufio.NewScanner(r)
	writer := bufio.NewWriter(w)
	defer writer.Flush()

	ask := func(prompt, defaultVal string) string {
		if defaultVal != "" {
			fmt.Fprintf(writer, "%s [%s]: ", prompt, defaultVal)
		} else {
			fmt.Fprintf(writer, "%s: ", prompt)
		}
		writer.Flush()
		if !scanner.Scan() {
			return defaultVal
		}
		val := strings.TrimSpace(scanner.Text())
		if val == "" {
			return defaultVal
		}
		return val
	}

	portStr := ask("Webhook listen port", "9527")
	port, _ := strconv.Atoi(portStr)
	if port == 0 {
		port = 9527
	}

	host := ask("Listen host", "0.0.0.0")

	name := ask("Service name", "")
	svcType := ask("Service type (springboot)", "springboot")
	repoURL := ask("Git repository URL", "")
	branch := ask("Deploy branch", "main")
	workspace := ask("Workspace directory", "")
	buildCmd := ask("Build command", "mvn package -DskipTests")
	runCmd := ask("Run command", "")

	smtpHost := ask("SMTP host (optional, e.g. smtp.qq.com)", "")
	smtpPortStr := ask("SMTP port", "")
	smtpPort, _ := strconv.Atoi(smtpPortStr)
	if smtpPort == 0 {
		smtpPortStr = "465"
		smtpPort, _ = strconv.Atoi(smtpPortStr)
	}
	smtpUser := ask("SMTP username", "")
	smtpToken := ask("SMTP token (authorization code)", "")
	smtpTLS := ask("Use TLS/SSL", "true")
	smtpTLSBool := smtpTLS == "true"

	notificationToInput := ask("Notification recipients (comma-separated, optional)", "")
	var notificationTo []string
	if notificationToInput != "" {
		for _, addr := range strings.Split(notificationToInput, ",") {
			addr = strings.TrimSpace(addr)
			if addr != "" {
				notificationTo = append(notificationTo, addr)
			}
		}
	}

	cfg := &AppConfig{
		Server: ServerConfig{Host: host, Port: port},
		SMTP:   SMTPConfig{Host: smtpHost, Port: smtpPort, Username: smtpUser, Token: smtpToken, TLS: smtpTLSBool},
		Notifications: NotificationConfig{To: notificationTo},
		Services: []ServiceConfig{{
			Name:      name,
			Type:      svcType,
			Repo:      RepoConfig{URL: repoURL, Branch: branch},
			Workspace: workspace,
			Build:     BuildConfig{Command: buildCmd},
			Run:       RunConfig{Command: runCmd},
		}},
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return err
	}

	fmt.Fprintf(writer, "\nConfiguration saved to %s\n", configPath)
	writer.Flush()
	return nil
}

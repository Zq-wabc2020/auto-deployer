package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// RunWizard runs an interactive wizard that prompts the user for configuration
// values and writes a YAML config file (new two-tier format) at configPath.
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
	svcType := ask("Service type (jvm/static/node/python/docker)", "jvm")
	repoURL := ask("Git repository URL", "")
	branch := ask("Deploy branch", "main")
	workspace := ask("Workspace directory", "")
	buildCmd := ask("Build command", defaultBuildCommand(svcType, name))

	runCmd := ""
	if svcType == "jvm" || svcType == "springboot" {
		runCmd = ask("Run command (pure launch, no nohup/&)", "java -jar target/"+name+".jar")
	}

	// deploy.health 所有模型必填：就绪判定与 status 探测共用。
	// 空输入时给出默认占位（http://localhost:<模型常用端口>/health），仍会写入配置，
	// 由后续 config 校验兜底提示。
	healthURL := ask("Health check URL (required, readiness + status probe)", defaultHealthURL(svcType))
	healthInterval := ask("Health poll interval (optional, default 10s)", "")

	smtpHost := ask("SMTP host (optional, e.g. smtp.qq.com)", "")
	smtpPortStr := ask("SMTP port", "465")
	smtpPort, _ := strconv.Atoi(smtpPortStr)
	if smtpPort == 0 {
		smtpPort = 465
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

	// Build YAML (new two-tier format) directly for full control of ordering
	// and comments.
	var b strings.Builder
	fmt.Fprintf(&b, "# deployd 全局配置\n\n")
	fmt.Fprintf(&b, "server:\n  host: %q\n  port: %d\n\n", host, port)
	fmt.Fprintf(&b, "webhook:\n  secret: \"\"\n\n")
	fmt.Fprintf(&b, "smtp:\n  host: %q\n  port: %d\n  username: %q\n  token: %q\n  tls: %v\n\n",
		smtpHost, smtpPort, smtpUser, smtpToken, smtpTLSBool)
	fmt.Fprintf(&b, "resend:\n  api_key: \"\"\n  from: \"\"\n\n")
	fmt.Fprintf(&b, "notifications:\n")
	if len(notificationTo) == 0 {
		fmt.Fprintf(&b, "  to: []\n\n")
	} else {
		fmt.Fprintf(&b, "  to:\n")
		for _, addr := range notificationTo {
			fmt.Fprintf(&b, "    - %q\n", addr)
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "services:\n")
	fmt.Fprintf(&b, "  - name: %q\n", name)
	fmt.Fprintf(&b, "    type: %q\n", svcType)
	fmt.Fprintf(&b, "    repo:\n      url: %q\n      branch: %q\n", repoURL, branch)
	fmt.Fprintf(&b, "    workspace: %q\n", workspace)
	fmt.Fprintf(&b, "    build:\n      command: %q\n", buildCmd)
	writeDeployBlock(&b, svcType, name, runCmd, healthURL, healthInterval)

	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(configPath, []byte(b.String()), 0644); err != nil {
		return err
	}

	fmt.Fprintf(writer, "\nConfiguration saved to %s\n", configPath)
	writer.Flush()
	return nil
}

// defaultBuildCommand returns a sensible build command default for a type.
func defaultBuildCommand(svcType, name string) string {
	switch svcType {
	case "jvm", "springboot":
		return "mvn package -DskipTests"
	case "static", "node":
		return "npm run build"
	case "python":
		return "pip install -r requirements.txt"
	case "docker":
		return "docker build -t " + name + ":latest ."
	}
	return ""
}

// defaultHealthURL 按模型给出常用端口的 health 占位默认值（用户仍应按实际改写）。
func defaultHealthURL(svcType string) string {
	switch svcType {
	case "jvm", "springboot":
		return "http://localhost:8080/health"
	case "static":
		return "https://example.com/health"
	case "node":
		return "http://localhost:3000/health"
	default: // python / docker
		return "http://localhost:8000/health"
	}
}

// writeDeployBlock emits the type-specific deploy: section. jvm uses the
// run command asked earlier; others emit a template for the user to fill in
// as their plugin lands. health/healthInterval 为所有模型通用字段，总是写入。
func writeDeployBlock(b *strings.Builder, svcType, name, runCmd, healthURL, healthInterval string) {
	writeCommon := func() {
		fmt.Fprintf(b, "      health: %q\n", healthURL)
		if healthInterval != "" {
			fmt.Fprintf(b, "      health_interval: %q\n", healthInterval)
		}
	}
	switch svcType {
	case "jvm", "springboot":
		fmt.Fprintf(b, "    deploy:\n      run: %q\n", runCmd)
		writeCommon()
	case "static":
		fmt.Fprintf(b, "    deploy:\n      artifact: \"dist/*\"\n      dest: \"/usr/share/nginx/html/%s\"\n      nginx_reload: true\n", name)
		writeCommon()
	case "node":
		fmt.Fprintf(b, "    deploy:\n      run: \"node server.js\"\n")
		writeCommon()
		fmt.Fprintf(b, "      env:\n        NODE_ENV: \"production\"\n")
	case "python":
		fmt.Fprintf(b, "    deploy:\n      venv: \".venv\"\n      run: \"uvicorn main:app --host 0.0.0.0 --port 8000\"\n      # migrate: \".venv/bin/alembic upgrade head\"\n")
		writeCommon()
	case "docker":
		fmt.Fprintf(b, "    deploy:\n      image: \"%s:latest\"\n      container: \"%s\"\n      ports: [\"8000:8000\"]\n", name, name)
		writeCommon()
	default:
		fmt.Fprintf(b, "    # deploy:\n    #   run: \"...\"\n")
		writeCommon()
	}
}

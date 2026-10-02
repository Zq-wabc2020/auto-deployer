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

// RunWizard 以交互方式引导用户填写配置，生成 pipeline 格式的 YAML 配置文件
// 并写到 configPath。
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

	name := ask("Work item name", "")
	repoURL := ask("Git repository URL", "")
	branch := ask("Deploy branch", "main")
	workspace := ask("Workspace directory", "")
	buildCmd := ask("Build command", "mvn package -DskipTests")
	deployCmd := ask("Deploy command (e.g. systemctl restart <service>)", "")

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

	// 直接手写 pipeline 格式 YAML，控制输出顺序与注释。
	var b strings.Builder
	fmt.Fprintf(&b, "# deployd 配置（由 deployd config 生成）\n\n")
	fmt.Fprintf(&b, "server:\n  host: %q\n  port: %d\n\n", host, port)
	fmt.Fprintf(&b, "smtp:\n  host: %q\n  port: %d\n  username: %q\n  token: %q\n  tls: %v\n\n",
		smtpHost, smtpPort, smtpUser, smtpToken, smtpTLSBool)
	fmt.Fprintf(&b, "notifications:\n")
	if len(notificationTo) == 0 {
		fmt.Fprintf(&b, "  to: []\n\n")
	} else {
		quoted := make([]string, 0, len(notificationTo))
		for _, addr := range notificationTo {
			quoted = append(quoted, fmt.Sprintf("%q", addr))
		}
		fmt.Fprintf(&b, "  to: [%s]\n\n", strings.Join(quoted, ", "))
	}
	fmt.Fprintf(&b, "pipelines:\n")
	fmt.Fprintf(&b, "  - name: %q\n", name)
	fmt.Fprintf(&b, "    workspace: %q\n", workspace)
	fmt.Fprintf(&b, "    timeout: \"45m\"\n")
	fmt.Fprintf(&b, "    stages:\n")
	// git 拉取节点：url/branch 是 webhook 匹配的字面量
	fmt.Fprintf(&b, "      - name: 拉取代码\n")
	fmt.Fprintf(&b, "        type: git\n")
	fmt.Fprintf(&b, "        params:\n")
	fmt.Fprintf(&b, "          url: %q\n", repoURL)
	fmt.Fprintf(&b, "          branch: [%q]\n", branch)
	// 构建节点
	fmt.Fprintf(&b, "      - name: 构建\n")
	fmt.Fprintf(&b, "        type: shell\n")
	fmt.Fprintf(&b, "        params:\n")
	fmt.Fprintf(&b, "          sh: %q\n", buildCmd)
	// 部署节点
	fmt.Fprintf(&b, "      - name: 部署\n")
	fmt.Fprintf(&b, "        type: shell\n")
	fmt.Fprintf(&b, "        params:\n")
	fmt.Fprintf(&b, "          sh: %q\n", deployCmd)
	// 主流程成功后通知
	fmt.Fprintf(&b, "      - name: 成功通知\n")
	fmt.Fprintf(&b, "        type: email\n")
	fmt.Fprintf(&b, "        params:\n")
	fmt.Fprintf(&b, "          subject: \"${system.name} 部署成功 ${system.commit}\"\n")
	fmt.Fprintf(&b, "          body: \"分支 ${system.branch} 由 ${system.author} 触发\"\n")
	// 主流程失败后通知
	fmt.Fprintf(&b, "      - name: 失败通知\n")
	fmt.Fprintf(&b, "        type: email\n")
	fmt.Fprintf(&b, "        when: failure\n")
	fmt.Fprintf(&b, "        params:\n")
	fmt.Fprintf(&b, "          subject: \"${system.name} 部署失败于 ${system.failed_stage}\"\n")
	fmt.Fprintf(&b, "          body: \"${system.message}\"\n")
	// 无论成败都清理工作空间
	fmt.Fprintf(&b, "      - name: 清理工作空间\n")
	fmt.Fprintf(&b, "        type: cleanup\n")
	fmt.Fprintf(&b, "        when: always\n")

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

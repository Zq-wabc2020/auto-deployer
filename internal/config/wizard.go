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
	repoToken := ask("Git access token (optional)", "")
	branch := ask("Deploy branch", "main")
	workspace := ask("Workspace directory", "")
	buildCmd := ask("Build command", "mvn package -DskipTests")
	runCmd := ask("Run command", "")

	cfg := &AppConfig{
		Server: ServerConfig{Host: host, Port: port},
		Services: []ServiceConfig{{
			Name:      name,
			Type:      svcType,
			Repo:      RepoConfig{URL: repoURL, Token: repoToken, Branch: branch},
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

package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type WebhookConfig struct {
	Secret string `yaml:"secret"`
}

type SMTPConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Token    string `yaml:"token"`
	TLS      bool   `yaml:"tls"`
}

type ResendConfig struct {
	APIKey string `yaml:"api_key"`
	From   string `yaml:"from"`
}

type NotificationConfig struct {
	To []string `yaml:"to"`
}

type RepoConfig struct {
	URL    string `yaml:"url"`
	Branch string `yaml:"branch"`
}

// Command is a build/run command that accepts either a single string or a list
// of strings in YAML. The list form runs commands sequentially.
type Command []string

// UnmarshalYAML allows Command to be parsed from a string or a list of strings.
func (c *Command) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var single string
	if err := unmarshal(&single); err == nil {
		*c = []string{single}
		return nil
	}
	var multi []string
	if err := unmarshal(&multi); err != nil {
		return err
	}
	*c = multi
	return nil
}

// MarshalYAML emits a single command as a string, multiple as a list.
func (c Command) MarshalYAML() (interface{}, error) {
	if len(c) == 1 {
		return c[0], nil
	}
	return []string(c), nil
}

// String joins commands with " && " for display and single-command execution.
func (c Command) String() string {
	return strings.Join(c, " && ")
}

// Slice returns the underlying command list.
func (c Command) Slice() []string {
	return c
}

// Empty reports whether no command is configured.
func (c Command) Empty() bool {
	return len(c) == 0
}

type BuildConfig struct {
	Command Command `yaml:"command"`
}

type ServiceConfig struct {
	Name      string     `yaml:"name"`
	Type      string     `yaml:"type"`
	Repo      RepoConfig `yaml:"repo"`
	Workspace string     `yaml:"workspace"`
	// Timeout is the TOTAL budget shared by fetch + build + stage (one
	// deadline spanning the whole deploy, not three per-stage budgets).
	// Go duration syntax (time.ParseDuration), e.g. "45m" or "90s". Empty =
	// default (30m). Stop/Start are not covered (Stop has its own 10s grace).
	Timeout string      `yaml:"timeout"`
	Build   BuildConfig `yaml:"build"`
	// Deploy is the raw strategy-layer node parsed by the type's plugin.
	// NOTE: must be a value yaml.Node (a *yaml.Node field gets allocated but
	// never filled by yaml.v3, silently dropping the deploy: block).
	// Zero value (Kind==0) means "no deploy block configured".
	Deploy yaml.Node `yaml:"deploy"`
}

// TimeoutDuration parses the service-level timeout. Returns 0 when unset; a
// parse error for an invalid value.
func (s ServiceConfig) TimeoutDuration() (time.Duration, error) {
	if s.Timeout == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s.Timeout)
	if err != nil {
		return 0, fmt.Errorf("invalid timeout %q: %w", s.Timeout, err)
	}
	return d, nil
}

type AppConfig struct {
	Server        ServerConfig       `yaml:"server"`
	Webhook       WebhookConfig      `yaml:"webhook"`
	SMTP          SMTPConfig         `yaml:"smtp"`
	Resend        ResendConfig       `yaml:"resend"`
	Notifications NotificationConfig `yaml:"notifications"`
	Services      []ServiceConfig    `yaml:"services"`
}

func Load(path string) (*AppConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	var cfg AppConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}
	warnUnknownFields(data)
	warnLegacyRunField(data)
	warnLegacyBuildTimeout(data)
	return &cfg, nil
}

// warnUnknownFields re-decodes the config with strict field checking and warns
// about fields the structs don't know. A misplaced key (e.g. `timeout` at
// service level instead of under `build:`) or a field valid for another
// deployment model would otherwise be silently ignored.
func warnUnknownFields(data []byte) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var strict AppConfig
	if err := dec.Decode(&strict); err != nil {
		fmt.Fprintf(os.Stderr, "[config] 警告: 配置中存在无法识别的字段（会被忽略，请检查缩进层级和字段名）:\n%v\n", err)
	}
}

// StrictDecodeDeploy decodes a raw deploy node into out with strict field
// checking, so a field valid for another model (e.g. artifact on a node
// service) surfaces as an error instead of being silently dropped. Used by
// plugins to warn about strategy-layer typos.
func StrictDecodeDeploy(node yaml.Node, out interface{}) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	if err := enc.Encode(&node); err != nil {
		return err
	}
	_ = enc.Close()
	dec := yaml.NewDecoder(&buf)
	dec.KnownFields(true)
	return dec.Decode(out)
}

// warnLegacyRunField prints a migration hint if any service still uses the
// removed top-level `run` field (now deploy.run). Warning only, non-fatal.
func warnLegacyRunField(data []byte) {
	var raw struct {
		Services []map[string]yaml.Node `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return
	}
	for i, svc := range raw.Services {
		if _, ok := svc["run"]; ok {
			fmt.Fprintf(os.Stderr, "[config] 警告: services[%d] 使用了已移除的顶层字段 `run`，请迁移到 `deploy.run`（见设计文档 §9.2）\n", i)
		}
	}
}

// warnLegacyBuildTimeout prints a migration hint if any service still uses
// build.timeout (now promoted to the service level as a total budget). The
// field would otherwise be silently dropped (defaulting to 30m). Warning only.
func warnLegacyBuildTimeout(data []byte) {
	var raw struct {
		Services []struct {
			Build map[string]yaml.Node `yaml:"build"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return
	}
	for i, svc := range raw.Services {
		if _, ok := svc.Build["timeout"]; ok {
			fmt.Fprintf(os.Stderr, "[config] 警告: services[%d] 的 build.timeout 已移到服务层，请改为与 build 同级的 timeout（fetch+build+stage 总超时）\n", i)
		}
	}
}

// DeploydDir returns the tool's state directory (~/.deployd), which holds the
// pid files, logs, and the daemon-recorded config path.
func DeploydDir() string {
	home := homeDir()
	return filepath.Join(home, ".deployd")
}

func homeDir() string {
	home := os.Getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return home
}

// RecordedConfigPathFile returns the file where the daemon records the config
// path it was started with. CLI commands (status/logs/deploy/...) use it as a
// fallback so they can find the config even when invoked from a directory
// outside the search path. Overwritten on every daemon start.
func RecordedConfigPathFile() string {
	return filepath.Join(DeploydDir(), "config.path")
}

// RecordConfigPath records the (absolute) config path the daemon started with.
func RecordConfigPath(configPath string) {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		abs = configPath
	}
	_ = os.MkdirAll(DeploydDir(), 0755)
	_ = os.WriteFile(RecordedConfigPathFile(), []byte(abs), 0644)
}

// DefaultConfig finds the default config file by priority:
//  1. Current directory config.yaml
//  2. ~/.deployd/config.yaml
//  3. The path recorded by the last `deployd start` (~/.deployd/config.path)
//  4. ~/config.yaml (legacy daemon default, kept for compatibility)
//
// Returns empty string if none found.
func DefaultConfig() string {
	// Check current directory
	if _, err := os.Stat("config.yaml"); err == nil {
		return "config.yaml"
	}
	// Check ~/.deployd/config.yaml
	path := filepath.Join(DeploydDir(), "config.yaml")
	if _, err := os.Stat(path); err == nil {
		return path
	}
	// Fallback: the config the daemon was (last) started with, so commands
	// like `status` work from any directory when the config lives elsewhere.
	if data, err := os.ReadFile(RecordedConfigPathFile()); err == nil {
		recorded := strings.TrimSpace(string(data))
		if recorded != "" {
			if _, err := os.Stat(recorded); err == nil {
				return recorded
			}
		}
	}
	// Legacy daemon default before config paths were unified.
	legacy := filepath.Join(homeDir(), defaultConfigName)
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return ""
}

const defaultConfigName = "config.yaml"

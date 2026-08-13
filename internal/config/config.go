package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// RunConfig retained temporarily for incremental migration; new configs use
// deploy.run (parsed by the plugin). Removed once the plugin switches.
type RunConfig struct {
	Command string `yaml:"command"`
}

type ServiceConfig struct {
	Name      string      `yaml:"name"`
	Type      string      `yaml:"type"`
	Repo      RepoConfig  `yaml:"repo"`
	Workspace string      `yaml:"workspace"`
	Build     BuildConfig `yaml:"build"`
	Run       RunConfig   `yaml:"run"`    // 保留以兼容渐进迁移；新配置改用 deploy.run
	Deploy    *yaml.Node  `yaml:"deploy"` // 策略层原始节点，由对应类型插件自解析
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
	return &cfg, nil
}

// DefaultConfig finds the default config file by priority:
// 1. Current directory config.yaml
// 2. ~/.deployd/config.yaml
// Returns empty string if none found.
func DefaultConfig() string {
	// Check current directory
	if _, err := os.Stat("config.yaml"); err == nil {
		return "config.yaml"
	}
	// Check ~/.deployd/config.yaml
	home := os.Getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	path := filepath.Join(home, ".deployd", "config.yaml")
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

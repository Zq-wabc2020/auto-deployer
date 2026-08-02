package config

import (
	"fmt"
	"os"
	"path/filepath"

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

type BuildConfig struct {
	Command string `yaml:"command"`
}

type RunConfig struct {
	Command string `yaml:"command"`
}

type ServiceConfig struct {
	Name      string      `yaml:"name"`
	Type      string      `yaml:"type"`
	Repo      RepoConfig  `yaml:"repo"`
	Workspace string      `yaml:"workspace"`
	Build     BuildConfig `yaml:"build"`
	Run       RunConfig   `yaml:"run"`
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

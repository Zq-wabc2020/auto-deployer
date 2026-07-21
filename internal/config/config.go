package config

import (
    "fmt"
    "os"

    "gopkg.in/yaml.v3"
)

type ServerConfig struct {
    Host string `yaml:"host"`
    Port int    `yaml:"port"`
}

type WebhookConfig struct {
    Secret string `yaml:"secret"`
}

type RepoConfig struct {
    URL    string `yaml:"url"`
    Token  string `yaml:"token"`
    Branch string `yaml:"branch"`
}

type BuildConfig struct {
    Command string `yaml:"command"`
}

type RunConfig struct {
    Command string `yaml:"command"`
}

type ServiceConfig struct {
    Name      string       `yaml:"name"`
    Type      string       `yaml:"type"`
    Repo      RepoConfig   `yaml:"repo"`
    Workspace string       `yaml:"workspace"`
    Build     BuildConfig  `yaml:"build"`
    Run       RunConfig    `yaml:"run"`
}

type AppConfig struct {
    Server   ServerConfig    `yaml:"server"`
    Webhook  WebhookConfig   `yaml:"webhook"`
    Services []ServiceConfig `yaml:"services"`
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

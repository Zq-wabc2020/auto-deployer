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
	// HealthURL/HealthInterval 从 deploy: 块中心解析（通用字段），供 orchestrator
	// 就绪轮询与 static.Status 读取。不在 yaml 顶层，故无 yaml tag。
	HealthURL      string `yaml:"-"`
	HealthInterval string `yaml:"-"`
}

// HealthIntervalDuration 解析 deploy.health_interval，空=默认 10s。
func (s ServiceConfig) HealthIntervalDuration() (time.Duration, error) {
	if s.HealthInterval == "" {
		return 10 * time.Second, nil
	}
	d, err := time.ParseDuration(s.HealthInterval)
	if err != nil {
		return 0, fmt.Errorf("invalid health_interval %q: %w", s.HealthInterval, err)
	}
	return d, nil
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

// StageConfig 是流水线的一个节点：以某组件类型执行，params 是组件入参
// （string 值在执行期做 ${...} 插值，非 string 值如 git 的 branch 列表原样透传）。
// When: ""=主流程 / "failure"=主流程失败时执行 / "always"=无论成败执行。
type StageConfig struct {
	Name    string            `yaml:"name"`
	Type    string            `yaml:"type"`
	Params  map[string]any    `yaml:"params"`
	Output  map[string]string `yaml:"output"`
	Skip    string            `yaml:"skip"`
	When    string            `yaml:"when"`
	Timeout string            `yaml:"timeout"`
}

func (s StageConfig) TimeoutDuration() (time.Duration, error) {
	if s.Timeout == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s.Timeout)
	if err != nil {
		return 0, fmt.Errorf("invalid stage timeout %q: %w", s.Timeout, err)
	}
	return d, nil
}

// PipelineConfig 是一个工作项：任意数量节点组成的流水线模板。
// TriggerURL/TriggerBranches 在加载期从第一个 git 节点提取，供 webhook 匹配；
// 无 git 节点的工作项只能手动 exec。
type PipelineConfig struct {
	Name      string            `yaml:"name"`
	Workspace string            `yaml:"workspace"`
	Timeout   string            `yaml:"timeout"`
	Env       map[string]string `yaml:"env"`
	Stages    []StageConfig     `yaml:"stages"`

	TriggerURL      string   `yaml:"-"`
	TriggerBranches []string `yaml:"-"`
}

func (p PipelineConfig) TimeoutDuration() (time.Duration, error) {
	if p.Timeout == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(p.Timeout)
	if err != nil {
		return 0, fmt.Errorf("invalid timeout %q: %w", p.Timeout, err)
	}
	return d, nil
}

// ExtractTrigger 从第一个 type==git 的节点提取 webhook 匹配用的 url/branch。
// 两项必须是字面量：匹配发生在配置加载期，那时还没有可插值的参数集。
func (p *PipelineConfig) ExtractTrigger() error {
	for _, st := range p.Stages {
		if st.Type != "git" {
			continue
		}
		url, _ := st.Params["url"].(string)
		if strings.Contains(url, "${") {
			return fmt.Errorf("pipeline %q: git 节点的 url 必须是字面量（webhook 匹配用），当前 %q", p.Name, url)
		}
		p.TriggerURL = url
		for _, b := range asAnyList(st.Params["branch"]) {
			if strings.Contains(b, "${") {
				return fmt.Errorf("pipeline %q: git 节点的 branch 必须是字面量，当前 %q", p.Name, b)
			}
			p.TriggerBranches = append(p.TriggerBranches, b)
		}
		return nil
	}
	return nil
}

func asAnyList(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// FindPipeline 按名查找工作项；找不到返回 nil。
func FindPipeline(cfg *AppConfig, name string) *PipelineConfig {
	for i := range cfg.Pipelines {
		if cfg.Pipelines[i].Name == name {
			return &cfg.Pipelines[i]
		}
	}
	return nil
}

type AppConfig struct {
	Server        ServerConfig       `yaml:"server"`
	Webhook       WebhookConfig      `yaml:"webhook"`
	SMTP          SMTPConfig         `yaml:"smtp"`
	Resend        ResendConfig       `yaml:"resend"`
	Notifications NotificationConfig `yaml:"notifications"`
	Services      []ServiceConfig    `yaml:"services"`
	Pipelines     []PipelineConfig   `yaml:"pipelines"`
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
	parseCommonDeployFields(&cfg)
	for i := range cfg.Pipelines {
		if err := cfg.Pipelines[i].ExtractTrigger(); err != nil {
			return nil, err
		}
	}
	return &cfg, nil
}

// parseCommonDeployFields 从每个服务的 deploy: 节点解出通用字段 health /
// health_interval，存到 svc.HealthURL / svc.HealthInterval。插件无需各自持有这些字段。
func parseCommonDeployFields(cfg *AppConfig) {
	for i := range cfg.Services {
		svc := &cfg.Services[i]
		if svc.Deploy.Kind == 0 {
			continue
		}
		var common struct {
			Health          string `yaml:"health"`
			HealthInterval string `yaml:"health_interval"`
		}
		_ = yaml.Unmarshal(encodeNode(svc.Deploy), &common) // 容错：失败则留空，校验会拦
		svc.HealthURL = common.Health
		svc.HealthInterval = common.HealthInterval
	}
}

// encodeNode 把 yaml.Node 编码回字节（parseCommonDeployFields 与 StrictDecodeDeploy 复用）。
func encodeNode(node yaml.Node) []byte {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	_ = enc.Encode(&node)
	_ = enc.Close()
	return buf.Bytes()
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
//
// 通用字段 health / health_interval 由中心层解析，不属于任何插件的策略结构体，
// 在严格解码前先剥除，避免触发「未知字段」误报。
func StrictDecodeDeploy(node yaml.Node, out interface{}) error {
	data := encodeNode(node)
	// 去除通用字段，避免插件结构体无这些字段时触发「未知字段」警告
	var m map[string]interface{}
	if err := yaml.Unmarshal(data, &m); err == nil {
		delete(m, "health")
		delete(m, "health_interval")
		if b, err := yaml.Marshal(m); err == nil {
			data = b
		}
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
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

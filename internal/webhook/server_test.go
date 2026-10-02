package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-deployer/auto-deployer/internal/config"
)

func TestHandle_GitHubPush(t *testing.T) {
	setSecretAndConfig(t, "")
	body := `{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/user/repo.git"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	Handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandle_GiteePush(t *testing.T) {
	setSecretAndConfig(t, "")
	body := `{"ref":"main","repository":{"git_http_url":"https://gitee.com/user/repo.git"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-Gitee-Event", "Push Hook")
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	Handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandle_GitHubPush_FormEncoded(t *testing.T) {
	// GitHub webhooks can be delivered as application/x-www-form-urlencoded
	// with the JSON payload in the "payload" form field.
	setSecretAndConfig(t, "")
	form := url.Values{}
	form.Set("payload", `{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/user/repo.git"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(form.Encode()))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rr := httptest.NewRecorder()
	Handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for form-encoded GitHub push, got %d", rr.Code)
	}
}

func TestHandle_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	rr := httptest.NewRecorder()
	Handle(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

// ── Webhook 签名校验 ──────────────────────────────────────────────

func TestHandle_GitHub_ValidSignature(t *testing.T) {
	setSecretAndConfig(t, "s3cr3t")
	body := []byte(`{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/user/repo.git"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", signGitHub("s3cr3t", body, "sha256"))

	rr := httptest.NewRecorder()
	Handle(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandle_GitHub_BadSignature(t *testing.T) {
	setSecretAndConfig(t, "s3cr3t")
	body := []byte(`{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/user/repo.git"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")

	rr := httptest.NewRecorder()
	Handle(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestHandle_GitHub_NoSignature_WithSecret(t *testing.T) {
	setSecretAndConfig(t, "s3cr3t")
	body := []byte(`{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/user/repo.git"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	Handle(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestHandle_GitHub_FormEncoded_ValidSignature(t *testing.T) {
	// 签名覆盖 form 原文（payload=...），不是其中 JSON。
	setSecretAndConfig(t, "s3cr3t")
	form := url.Values{}
	form.Set("payload", `{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/user/repo.git"}}`)
	raw := []byte(form.Encode())
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(raw))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Hub-Signature-256", signGitHub("s3cr3t", raw, "sha256"))

	rr := httptest.NewRecorder()
	Handle(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandle_Gitee_ValidToken(t *testing.T) {
	setSecretAndConfig(t, "s3cr3t")
	body := []byte(`{"ref":"main","repository":{"git_http_url":"https://gitee.com/user/repo.git"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Gitee-Event", "Push Hook")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gitee-Token", "s3cr3t")

	rr := httptest.NewRecorder()
	Handle(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandle_Gitee_ValidSignatureTimestamp(t *testing.T) {
	setSecretAndConfig(t, "s3cr3t")
	body := []byte(`{"ref":"main","repository":{"git_http_url":"https://gitee.com/user/repo.git"}}`)
	ts := "1730000000000"
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Gitee-Event", "Push Hook")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gitee-Timestamp", ts)
	req.Header.Set("X-Gitee-Signature", signGitee("s3cr3t", ts))

	rr := httptest.NewRecorder()
	Handle(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandle_Gitee_WrongToken(t *testing.T) {
	setSecretAndConfig(t, "s3cr3t")
	body := []byte(`{"ref":"main","repository":{"git_http_url":"https://gitee.com/user/repo.git"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Gitee-Event", "Push Hook")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gitee-Token", "wrong")

	rr := httptest.NewRecorder()
	Handle(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestHandle_UnknownSource_WithSecret(t *testing.T) {
	setSecretAndConfig(t, "s3cr3t")
	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	Handle(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unknown source with secret, got %d", rr.Code)
	}
}

func TestHandle_ConfigErrorIs500(t *testing.T) {
	// 配置损坏不能再宽松回 200——让平台侧看到 5xx 并重试。
	SetConfigPath(filepath.Join(t.TempDir(), "missing.yaml"))
	defer SetConfigPath("")
	body := `{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/user/repo.git"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	Handle(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rr.Code)
	}
}

// verifySecret 单元测试（直击签名逻辑）。
func TestVerifySecret_GitHubSHA256(t *testing.T) {
	body := []byte(`{"a":1}`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set(githubSig256Header, signGitHub("k", body, "sha256"))
	if err := verifySecret(req, body, "github", "k"); err != nil {
		t.Errorf("valid sha256 rejected: %v", err)
	}
}

func TestVerifySecret_GitHubSHA256_TamperedBody(t *testing.T) {
	// 签名按原始 body 计算，投递的却是被篡改的原文 → 校验必须失败。
	body := []byte(`{"a":1}`)
	tampered := []byte(`{"a":2}`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(tampered))
	req.Header.Set(githubSig256Header, signGitHub("k", body, "sha256"))
	if err := verifySecret(req, tampered, "github", "k"); err == nil {
		t.Error("tampered body must fail")
	}
}

func TestVerifySecret_GitHubSHA256_WrongSecret(t *testing.T) {
	body := []byte(`{"a":1}`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set(githubSig256Header, signGitHub("other", body, "sha256"))
	if err := verifySecret(req, body, "github", "k"); err == nil {
		t.Error("wrong secret must fail")
	}
}

func TestVerifySecret_GitHubSHA1(t *testing.T) {
	body := []byte(`{"a":1}`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set(githubSigHeader, signGitHub("k", body, "sha1"))
	if err := verifySecret(req, body, "github", "k"); err != nil {
		t.Errorf("valid sha1 rejected: %v", err)
	}
}

func TestVerifySecret_GitHubSHA1_Tampered(t *testing.T) {
	body := []byte(`{"a":1}`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set(githubSigHeader, signGitHub("k", []byte(`{"a":9}`), "sha1"))
	if err := verifySecret(req, body, "github", "k"); err == nil {
		t.Error("tampered sha1 must fail")
	}
}

func TestVerifySecret_GitHub_BadSHA256_NoSHA1Downgrade(t *testing.T) {
	// sha256 头存在但不匹配（哪怕是攻击者提供的），绝不回退到正确的 sha1。
	body := []byte(`{"a":1}`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set(githubSig256Header, "sha256=ffff")
	req.Header.Set(githubSigHeader, signGitHub("k", body, "sha1"))
	if err := verifySecret(req, body, "github", "k"); err == nil {
		t.Error("wrong sha256 + right sha1 must fail (no downgrade)")
	}
}

func TestVerifySecret_GitHub_MissingHeaders(t *testing.T) {
	body := []byte(`{"a":1}`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	if err := verifySecret(req, body, "github", "k"); err == nil {
		t.Error("missing signature must fail")
	}
}

func TestVerifySecret_Gitee_Token(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set(giteeTokenHeader, "seekrit")
	if err := verifySecret(req, nil, "gitee", "seekrit"); err != nil {
		t.Errorf("valid token rejected: %v", err)
	}
}

func TestVerifySecret_Gitee_WrongToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set(giteeTokenHeader, "nope")
	if err := verifySecret(req, nil, "gitee", "seekrit"); err == nil {
		t.Error("wrong token must fail")
	}
}

func TestVerifySecret_Gitee_Signature(t *testing.T) {
	ts := "1730000000000"
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set(giteeTimestampHeader, ts)
	req.Header.Set(giteeSigHeader, signGitee("seekrit", ts))
	if err := verifySecret(req, nil, "gitee", "seekrit"); err != nil {
		t.Errorf("valid signature rejected: %v", err)
	}
}

func TestVerifySecret_Gitee_TimestampOnly(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set(giteeTimestampHeader, "1730000000000")
	if err := verifySecret(req, nil, "gitee", "seekrit"); err == nil {
		t.Error("timestamp without signature must fail")
	}
}

func TestVerifySecret_Gitee_TamperedSignature(t *testing.T) {
	ts := "1730000000000"
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set(giteeTimestampHeader, ts)
	req.Header.Set(giteeSigHeader, signGitee("seekrit", "1730000000001"))
	if err := verifySecret(req, nil, "gitee", "seekrit"); err == nil {
		t.Error("tampered signature must fail")
	}
}

func TestVerifySecret_EmptySecret_Permissive(t *testing.T) {
	// 未配置 secret = 不校验（逃生口），任何来源/签名都放行。
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if err := verifySecret(req, nil, "github", ""); err != nil {
		t.Error("empty secret must be permissive")
	}
	if err := verifySecret(req, nil, "gitee", ""); err != nil {
		t.Error("empty secret must be permissive")
	}
}

// ── 辅助 ──────────────────────────────────────────────────────────

// setSecretAndConfig 写一个只有 webhook.secret 的临时配置并指向它；
// 空 pipelines 让 Handle 校验通过后直接 200 ok，只测验证路径。
func setSecretAndConfig(t *testing.T, secret string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := fmt.Sprintf("webhook:\n  secret: %q\npipelines: []\n", secret)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	SetConfigPath(path)
	t.Cleanup(func() { SetConfigPath("") })
}

// signGitHub 按 GitHub 格式生成签名头值（sha256=/sha1=<hex>，对 body 计算 HMAC）。
func signGitHub(secret string, body []byte, alg string) string {
	if alg == "sha1" {
		mac := hmac.New(sha1.New, []byte(secret))
		mac.Write(body)
		return "sha1=" + hex.EncodeToString(mac.Sum(nil))
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// signGitee 按 Gitee 加签格式生成签名值（对时间戳做 HMAC-SHA256 的 hex）。
func signGitee(secret, ts string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestParsePayload_GitHub(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/develop","repository":{"clone_url":"https://github.com/user/repo.git"}}`)
	result, err := ParsePayload(body, "github")
	if err != nil {
		t.Fatal(err)
	}
	if result.Branch != "develop" {
		t.Errorf("expected branch develop, got %s", result.Branch)
	}
	if result.RepoURL != "https://github.com/user/repo.git" {
		t.Errorf("unexpected repo URL: %s", result.RepoURL)
	}
}

func TestParsePayload_Gitee(t *testing.T) {
	body := []byte(`{"ref":"master","repository":{"git_http_url":"https://gitee.com/user/repo.git"}}`)
	result, err := ParsePayload(body, "gitee")
	if err != nil {
		t.Fatal(err)
	}
	if result.Branch != "master" {
		t.Errorf("expected branch master, got %s", result.Branch)
	}
}

func TestParsePayload_GitHubWithCommits(t *testing.T) {
	body := []byte(`{
		"ref":"refs/heads/main",
		"repository":{"clone_url":"https://github.com/user/repo.git"},
		"commits":[{"author":{"email":"dev@example.com","name":"Dev"},"committer":{"email":"committer@example.com","name":"Committer"}}]
	}`)
	result, err := ParsePayload(body, "github")
	if err != nil {
		t.Fatal(err)
	}
	if result.AuthorEmail != "dev@example.com" {
		t.Errorf("expected dev@example.com, got %s", result.AuthorEmail)
	}
}

func TestParsePayload_GiteeWithCommits(t *testing.T) {
	body := []byte(`{
		"ref":"main",
		"repository":{"git_http_url":"https://gitee.com/user/repo.git"},
		"commits":[{"author":{"email":"dev@gitee.com","name":"Dev"}}]
	}`)
	result, err := ParsePayload(body, "gitee")
	if err != nil {
		t.Fatal(err)
	}
	if result.AuthorEmail != "dev@gitee.com" {
		t.Errorf("expected dev@gitee.com, got %s", result.AuthorEmail)
	}
}

func TestParsePayload_NoCommits(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/user/repo.git"}}`)
	result, err := ParsePayload(body, "github")
	if err != nil {
		t.Fatal(err)
	}
	if result.AuthorEmail != "" {
		t.Errorf("expected empty author email, got %s", result.AuthorEmail)
	}
}

func TestParsePayload_UnknownSource(t *testing.T) {
	body := []byte("{}")
	_, err := ParsePayload(body, "unknown")
	if err == nil {
		t.Fatal("expected error for unknown source")
	}
}

func TestDetectSource_GitHub(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-GitHub-Event", "push")
	if detectSource(req) != "github" {
		t.Error("expected github source")
	}
}

func TestDetectSource_Gitee(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Gitee-Event", "Push Hook")
	if detectSource(req) != "gitee" {
		t.Error("expected gitee source")
	}
}

func TestMatchPipelines(t *testing.T) {
	pipelines := []config.PipelineConfig{
		{Name: "app", TriggerURL: "https://github.com/u/r.git", TriggerBranches: []string{"main"}},
		{Name: "app-glob", TriggerURL: "https://github.com/u/r.git", TriggerBranches: []string{"release/*"}},
		{Name: "other-repo", TriggerURL: "https://github.com/u/other.git", TriggerBranches: []string{"main"}},
		{Name: "manual-only", TriggerURL: "", TriggerBranches: nil}, // 无 git 节点
	}
	cases := []struct {
		branch string
		want   []string
	}{
		{"main", []string{"app"}},             // 精确命中
		{"release/1.2", []string{"app-glob"}}, // glob 命中
		{"dev", nil},                          // 无命中
	}
	for _, c := range cases {
		got := MatchPipelines(pipelines, &DispatchResult{
			RepoURL: "https://github.com/u/r.git", Branch: c.branch,
		})
		var names []string
		for _, p := range got {
			names = append(names, p.Name)
		}
		if fmt.Sprint(names) != fmt.Sprint(c.want) {
			t.Errorf("branch=%s got %v want %v", c.branch, names, c.want)
		}
	}
}

func TestParsePayloadCommitMessage(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/u/r.git"},
		"commits":[{"id":"abc123def","message":"fix: something","author":{"email":"a@b.c"}}]}`)
	res, err := ParsePayload(body, "github")
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit != "abc123def" || res.Message != "fix: something" || res.AuthorEmail != "a@b.c" {
		t.Fatalf("res=%+v", res)
	}
}

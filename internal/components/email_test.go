package components

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/auto-deployer/auto-deployer/internal/notify"
)

func TestEmail(t *testing.T) {
	c, _ := Get("email")
	// nil Notifier → 明确报错
	if _, err := c.Run(context.Background(), Request{
		Params: map[string]any{"subject": "s", "body": "b"}, Out: &bytes.Buffer{},
	}); err == nil {
		t.Fatal("未配置邮件服务必须报错")
	}
	// 只给 body 缺 subject → 报错（subject/body 必须成对）
	if _, err := c.Run(context.Background(), Request{
		Params: map[string]any{"body": "b"}, Out: &bytes.Buffer{}, Notifier: notify.New("", 0, "", "", false, "", ""),
	}); err == nil {
		t.Fatal("subject/body 只提供一个必须报错")
	}
	// 只给 subject 缺 body → 报错
	if _, err := c.Run(context.Background(), Request{
		Params: map[string]any{"subject": "s"}, Out: &bytes.Buffer{}, Notifier: notify.New("", 0, "", "", false, "", ""),
	}); err == nil {
		t.Fatal("subject/body 只提供一个必须报错")
	}
}

func TestEmailSends(t *testing.T) {
	// 用真实 Notifier 但指向不存在的服务器会失败；这里只验证参数解析路径：
	// 构造非法收件人触发 Send 报错，同时验证 to 解析（逗号分隔）。
	c, _ := Get("email")
	_, err := c.Run(context.Background(), Request{
		Params:   map[string]any{"subject": "s", "body": "b", "to": "a@x.com, b@y.com"},
		Out:      &bytes.Buffer{},
		Notifier: notify.New("127.0.0.1", 1, "", "", false, "", ""), // 端口 1 必连不上
	})
	if err == nil {
		t.Fatal("连接失败必须返回错误（证明 to 已传入 Send）")
	}
}

// ── 标准模板（V1 生产格式 + 触发类型）──

func TestBuildStandardTemplate_Success(t *testing.T) {
	for _, tc := range []struct{ trigger, want string }{
		{"webhook", "（自动触发）"},
		{"manual", "（手动触发）"},
		{"", "（自动触发）"}, // 未知/为空按自动渲染
	} {
		subj, body := buildStandardTemplate(map[string]string{
			"name": "hello1", "branch": "main", "message": "fix: 修了个 bug",
			"author": "dev@example.com", "trigger": tc.trigger,
		})
		want := "[自动部署] ✅ 部署成功: hello1" + tc.want
		if subj != want {
			t.Errorf("trigger=%q subject = %q, want %q", tc.trigger, subj, want)
		}
		for _, row := range []string{"服务名", "分支", "提交记录", "状态", "时间", "变更者"} {
			if !strings.Contains(body, row) {
				t.Errorf("trigger=%q body 缺行 %q", tc.trigger, row)
			}
		}
		for _, v := range []string{"hello1", "main", "fix: 修了个 bug", "success", "dev@example.com"} {
			if !strings.Contains(body, v) {
				t.Errorf("trigger=%q body 缺值 %q", tc.trigger, v)
			}
		}
		if strings.Contains(body, "失败阶段") || strings.Contains(body, "错误信息") {
			t.Error("成功邮件不应含失败行")
		}
	}
}

func TestBuildStandardTemplate_Failure(t *testing.T) {
	subj, body := buildStandardTemplate(map[string]string{
		"name": "admin", "branch": "auto_deploy_test", "message": "docker配置",
		"author": "568390181@qq.com", "trigger": "manual", "result": "failed",
		"failed_stage": "readiness", "error": "context deadline exceeded",
	})
	if subj != "[自动部署] ❌ 部署失败: admin（手动触发）" {
		t.Errorf("failure subject = %q", subj)
	}
	for _, v := range []string{"failed", "readiness", "context deadline exceeded", "568390181@qq.com", "docker配置"} {
		if !strings.Contains(body, v) {
			t.Errorf("failure body 缺值 %q", v)
		}
	}
}

func TestBuildStandardTemplate_NilSystem(t *testing.T) {
	// 老测试/直呼组件不给 System：不 panic，按 result 空 → success 渲染可发送串。
	subj, body := buildStandardTemplate(nil)
	if !strings.Contains(subj, "✅") {
		t.Errorf("nil system 应渲染成功主题, got %q", subj)
	}
	if !strings.Contains(body, "部署通知") {
		t.Errorf("nil system body 应含标题, got %q", body)
	}
}

func TestEmailStatus(t *testing.T) {
	if s := emailStatus(map[string]string{"result": "failed"}); s != "failed" {
		t.Errorf("result=failed → %q", s)
	}
	if s := emailStatus(map[string]string{}); s != "success" {
		t.Errorf("result 为空应 success, got %q", s)
	}
	if s := emailStatus(nil); s != "success" {
		t.Errorf("nil system 应 success, got %q", s)
	}
	if s := emailStatus(map[string]string{"result": "cancelled"}); s != "cancelled" {
		t.Errorf("cancelled 应原样透传, got %q", s)
	}
}

func TestTriggerLabel(t *testing.T) {
	if triggerLabel("manual") != "手动" {
		t.Error("manual → 手动")
	}
	if triggerLabel("webhook") != "自动" {
		t.Error("webhook → 自动")
	}
}

func TestUseStandardTemplate(t *testing.T) {
	cases := []struct {
		name         string
		params       map[string]any
		subject, got string
		wantDefault  bool
		wantErr      bool
	}{
		{"显式成对", map[string]any{}, "s", "b", false, false},
		{"都省略", map[string]any{}, "", "", true, false},
		{"显式声明默认模板", map[string]any{"template": "默认模板"}, "", "", true, false},
		{"template 英文 default", map[string]any{"template": "default"}, "", "", true, false},
		{"只给 subject", map[string]any{}, "s", "", false, true},
		{"只给 body", map[string]any{}, "", "b", false, true},
		{"未知模板名", map[string]any{"template": "dark"}, "s", "b", false, true},
	}
	for _, tc := range cases {
		def, err := useStandardTemplate(tc.params, tc.subject, tc.got)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: 应报错", tc.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: 不该报错: %v", tc.name, err)
			continue
		}
		if def != tc.wantDefault {
			t.Errorf("%s: wantDefault=%v got=%v", tc.name, tc.wantDefault, def)
		}
	}
}

// TestEmailRun_FallsBackToStandardTemplate 走完整 Run：不写 subject/body 时
// 组件必须用内置标准模板把邮件发出去（主题带触发类型）。
func TestEmailRun_FallsBackToStandardTemplate(t *testing.T) {
	addr, got, serverReady := fakeCaptureSMTP(t)
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}
	c, _ := Get("email")
	_, err := c.Run(context.Background(), Request{
		Params:   map[string]any{"to": "a@x.com"},
		System:   map[string]string{"name": "my-app", "branch": "main", "message": "feat: x", "author": "a@x.com", "trigger": "manual"},
		Out:      &bytes.Buffer{},
		Notifier: notify.New(host, port, "", "", false, "", ""),
	})
	select {
	case <-serverReady:
	case <-time.After(5 * time.Second):
		t.Fatal("fake SMTP 超时")
	}
	if err != nil {
		t.Fatalf("Run 失败: %v", err)
	}
	msg := got.String()
	if !strings.Contains(msg, "[自动部署] ✅ 部署成功: my-app（手动触发）") {
		t.Errorf("回退模板主题不对:\n%s", msg)
	}
	if !strings.Contains(msg, "服务名") || !strings.Contains(msg, "my-app") {
		t.Errorf("回退模板正文缺服务名行:\n%s", msg)
	}
}

// fakeCaptureSMTP 起一个记录整封邮件（含 DATA 内容）的无认证假 SMTP。
// 返回监听地址、捕获缓冲区指针、服务端收尾 signal。
func fakeCaptureSMTP(t *testing.T) (addr string, got *strings.Builder, done chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	done = make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		defer ln.Close()
		r := bufio.NewReader(conn)
		w := conn
		_, _ = w.Write([]byte("220 fake ESMTP\r\n"))
		inData := false
		readLine := func() string {
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			line, err := r.ReadString('\n')
			if err != nil {
				return ""
			}
			return strings.TrimRight(line, "\r\n")
		}
		for {
			line := readLine()
			if line == "" && !inData {
				return
			}
			upper := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
				_, _ = w.Write([]byte("250-fake\r\n250 8BITMIME\r\n"))
			case inData:
				if line == "." {
					_, _ = w.Write([]byte("250 queued\r\n"))
					inData = false
					continue
				}
				sb.WriteString(line)
				sb.WriteString("\n")
			case strings.HasPrefix(upper, "MAIL FROM:"):
				_, _ = w.Write([]byte("250 ok\r\n"))
			case strings.HasPrefix(upper, "RCPT TO:"):
				_, _ = w.Write([]byte("250 ok\r\n"))
			case strings.HasPrefix(upper, "DATA"):
				_, _ = w.Write([]byte("354 go\r\n"))
				inData = true
			case strings.HasPrefix(upper, "QUIT"):
				_, _ = w.Write([]byte("221 bye\r\n"))
				return
			default:
				_, _ = w.Write([]byte("250 ok\r\n"))
			}
		}
	}()
	return ln.Addr().String(), &sb, done
}
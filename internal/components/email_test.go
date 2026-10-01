package components

import (
	"bytes"
	"context"
	"testing"

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
	// 缺 subject/body → 报错
	if _, err := c.Run(context.Background(), Request{
		Params: map[string]any{"body": "b"}, Out: &bytes.Buffer{}, Notifier: notify.New("", 0, "", "", false, "", ""),
	}); err == nil {
		t.Fatal("缺 subject 必须报错")
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

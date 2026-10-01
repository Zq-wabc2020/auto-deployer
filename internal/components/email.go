package components

import (
	"context"
	"fmt"
	"strings"
)

func init() { Register("email", Email{}) }

// Email 发送通知邮件。SMTP/Resend 凭据来自全局配置（引擎注入 Notifier），
// 组件只负责 subject/body/to（全部支持参数插值，由引擎完成）。
type Email struct{}

func (Email) Run(ctx context.Context, req Request) (Results, error) {
	if req.Notifier == nil {
		return nil, fmt.Errorf("未配置 SMTP/Resend，email 组件不可用")
	}
	subject := asString(req.Params, "subject")
	body := asString(req.Params, "body")
	toRaw := asString(req.Params, "to")
	if subject == "" || body == "" {
		return nil, fmt.Errorf("email 组件缺少必填参数 subject/body")
	}
	var to []string
	for _, addr := range strings.Split(toRaw, ",") {
		if addr = strings.TrimSpace(addr); addr != "" {
			to = append(to, addr)
		}
	}
	if len(to) == 0 {
		return nil, fmt.Errorf("email 组件收件人为空（to 参数或全局 notifications.to 未配置）")
	}
	if err := req.Notifier.Send(to, subject, body); err != nil {
		return Results{"error": err.Error()}, fmt.Errorf("发送邮件失败: %w", err)
	}
	return Results{}, nil
}

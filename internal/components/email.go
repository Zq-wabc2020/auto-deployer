package components

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func init() { Register("email", Email{}) }

// Email 发送通知邮件。SMTP/Resend 凭据来自全局配置（引擎注入 Notifier），
// 组件只负责 subject/body/to（全部支持参数插值，由引擎完成）。
//
// 主题/正文三态（用户定稿的优先级）：
//   - subject 与 body 都提供 → 插值后原样使用（配置显式优先）；
//   - 都省略 → 内置标准模板（V1 生产格式，见 buildStandardTemplate）；
//     也可用 template: "default" / "默认模板" 显式声明走默认模板（与省略等价）；
//   - 只提供一个 → 报错（显式必须成对，防止漏写一边静默变默认）。
type Email struct{}

func (Email) Run(ctx context.Context, req Request) (Results, error) {
	if req.Notifier == nil {
		return nil, fmt.Errorf("未配置 SMTP/Resend，email 组件不可用")
	}
	subject := asString(req.Params, "subject")
	body := asString(req.Params, "body")
	toRaw := asString(req.Params, "to")

	useDefault, err := useStandardTemplate(req.Params, subject, body)
	if err != nil {
		return nil, err
	}
	if useDefault {
		subject, body = buildStandardTemplate(req.System)
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

// useStandardTemplate 按「配置显式 > 默认模板」优先级返回是否走内置模板。
// template 参数只校验取值（default / 默认模板 同义），实际是否用默认模板
// 由 subject/body 是否成对给出决定。
func useStandardTemplate(params map[string]any, subject, body string) (bool, error) {
	if tmpl := asString(params, "template"); tmpl != "" && tmpl != "default" && tmpl != "默认模板" {
		return false, fmt.Errorf("email 组件不认识的模板 %q（可选值: default / 默认模板；都不写时省略 subject/body 即用默认模板）", tmpl)
	}
	switch {
	case subject != "" && body != "":
		return false, nil // 配置显式优先
	case subject != "" || body != "":
		return false, fmt.Errorf("email 组件的 subject/body 必须成对提供，或同时省略用默认模板")
	default:
		return true, nil
	}
}

// buildStandardTemplate 生成 V1 生产格式的默认邮件（用户要求「和之前保持一样」）：
// 主题 [自动部署] ✅/❌ 部署成功/失败: <name>（<手动/自动触发>）；
// 正文 HTML 表格行序：服务名/分支/提交记录/状态/时间/变更者
// （失败邮件追加 失败阶段/错误信息）。「提交记录」= commit 说明，
// 「变更者」= 作者邮箱（空则整行省略），「时间」= 发送时刻。
func buildStandardTemplate(sys map[string]string) (subject, body string) {
	status := emailStatus(sys)
	label := "成功"
	mark := "✅"
	if status == "failed" {
		label = "失败"
		mark = "❌"
	}
	subject = fmt.Sprintf("[自动部署] %s 部署%s: %s（%s触发）",
		mark, label, sys["name"], triggerLabel(sys["trigger"]))

	var sb strings.Builder
	sb.WriteString("<html><body style='font-family: sans-serif;'>")
	sb.WriteString("<h2>部署通知</h2>")
	sb.WriteString("<table border='0' cellpadding='4' cellspacing='0' style='border-collapse: collapse;'>")
	sb.WriteString(emailRow("服务名", sys["name"]))
	sb.WriteString(emailRow("分支", sys["branch"]))
	sb.WriteString(emailRow("提交记录", sys["message"]))
	sb.WriteString(emailRow("状态", status))
	sb.WriteString(emailRow("时间", time.Now().Format("2006-01-02 15:04:05")))
	if author := sys["author"]; author != "" {
		sb.WriteString(emailRow("变更者", author))
	}
	if status == "failed" {
		sb.WriteString(emailRow("失败阶段", sys["failed_stage"]))
		sb.WriteString(emailRow("错误信息", sys["error"]))
	}
	sb.WriteString("</table>")
	sb.WriteString("</body></html>")
	return subject, sb.String()
}

// emailStatus 取渲染用状态：主流程成功邮件节点时 system.result 尚未写入
// （后置流程前才设），约定空 = success；其余原样透传（failed/cancelled）。
func emailStatus(sys map[string]string) string {
	if sys == nil {
		return "success"
	}
	if s := sys["result"]; s != "" {
		return s
	}
	return "success"
}

// triggerLabel 把 system.trigger 映射为邮件里的 手动/自动。
func triggerLabel(t string) string {
	if t == "manual" {
		return "手动"
	}
	return "自动"
}

// emailRow 复用 V1 表格行样式（docs/superpowers/plans/2026-07-24-email-notification.md）。
func emailRow(label, value string) string {
	return fmt.Sprintf("<tr><td style='padding:4px 8px;border:1px solid #ddd;background:#f5f5f5;font-weight:bold;'>%s</td><td style='padding:4px 8px;border:1px solid #ddd;'>%s</td></tr>", label, value)
}
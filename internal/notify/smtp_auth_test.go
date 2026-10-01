package notify

import (
	"bufio"
	"encoding/base64"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeSMTPServer 起一个最小 SMTP 对话服务器（非 TLS）。
// supportAuth 决定 EHLO 是否宣告 AUTH PLAIN；收到的命令与 AUTH 凭据记录在
// sawAuth / authUser / authPass 里，供断言。
func fakeSMTPServer(t *testing.T, supportAuth bool) (addr string, sawAuth *bool, authUser, authPass *string, gotMail *string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	saw, user, pass, mail := false, "", "", ""

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		defer ln.Close()
		r := bufio.NewReader(conn)
		w := conn
		w.Write([]byte("220 fake ESMTP\r\n"))
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
			if line == "" {
				return
			}
			upper := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
				if supportAuth {
					w.Write([]byte("250-fake\r\n250 AUTH PLAIN\r\n"))
				} else {
					w.Write([]byte("250-fake\r\n250 8BITMIME\r\n"))
				}
			case strings.HasPrefix(upper, "AUTH"):
				saw = true
				// AUTH PLAIN <base64>
				parts := strings.SplitN(line, " ", 3)
				if len(parts) == 3 {
					if raw, err := base64.StdEncoding.DecodeString(parts[2]); err == nil {
						// PLAIN 格式: identity\x00username\x00password
						f := strings.Split(string(raw), "\x00")
						if len(f) == 3 {
							user, pass = f[1], f[2]
						}
					}
				}
				w.Write([]byte("235 ok\r\n"))
			case strings.HasPrefix(upper, "MAIL FROM:"):
				mail = line
				w.Write([]byte("250 ok\r\n"))
			case strings.HasPrefix(upper, "RCPT TO:"):
				w.Write([]byte("250 ok\r\n"))
			case strings.HasPrefix(upper, "DATA"):
				w.Write([]byte("354 go\r\n"))
				for {
					d := readLine()
					if d == "." {
						break
					}
				}
				w.Write([]byte("250 queued\r\n"))
			case strings.HasPrefix(upper, "QUIT"):
				w.Write([]byte("221 bye\r\n"))
				return
			default:
				w.Write([]byte("250 ok\r\n"))
			}
		}
	}()

	sawAuth, authUser, authPass, gotMail = &saw, &user, &pass, &mail
	return ln.Addr().String(), sawAuth, authUser, authPass, gotMail
}

// 空用户名（无认证内网中继）时不应尝试 AUTH，邮件照常发出。
func TestSendSMTP_SkipsAuthWhenNoUsername(t *testing.T) {
	addr, sawAuth, _, _, gotMail := fakeSMTPServer(t, false)
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}

	n := New(host, port, "", "", false, "", "")
	if err := n.Send([]string{"a@b.com"}, "subject", "body"); err != nil {
		t.Fatalf("send failed: %v", err)
	}
	if *sawAuth {
		t.Error("AUTH should not be attempted when username is empty")
	}
	if !strings.HasPrefix(*gotMail, "MAIL FROM:") {
		t.Errorf("expected MAIL FROM to be issued, got: %q", *gotMail)
	}
}

// 有用户名时 AUTH PLAIN 的凭据必须正确（用户名/授权码——历史 bug 曾把用户名当密码）。
func TestSendSMTP_PlainAuthCredentials(t *testing.T) {
	addr, sawAuth, authUser, authPass, _ := fakeSMTPServer(t, true)
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}

	n := New(host, port, "user@test", "token123", false, "", "")
	if err := n.Send([]string{"a@b.com"}, "subject", "body"); err != nil {
		t.Fatalf("send failed: %v", err)
	}
	if !*sawAuth {
		t.Fatal("AUTH should be attempted when username is set")
	}
	if *authUser != "user@test" {
		t.Errorf("PLAIN username = %q, want user@test", *authUser)
	}
	if *authPass != "token123" {
		t.Errorf("PLAIN password = %q, want token123", *authPass)
	}
}

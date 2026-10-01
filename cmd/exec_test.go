package cmd

import "testing"

func TestParseExecArgs(t *testing.T) {
	name, kv, err := parseExecArgs([]string{"app", "--env=prod", "debug=true"})
	if err != nil || name != "app" || kv["env"] != "prod" || kv["debug"] != "true" {
		t.Fatalf("name=%q kv=%v err=%v", name, kv, err)
	}
	if _, _, err := parseExecArgs([]string{"app", "no-equals"}); err == nil {
		t.Fatal("无 = 的参数必须报错")
	}
	if _, _, err := parseExecArgs([]string{}); err == nil {
		t.Fatal("缺工作项名必须报错")
	}
}

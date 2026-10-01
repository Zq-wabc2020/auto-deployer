package param

import "testing"

func newTestSet() *Set {
	s := New()
	s.System = map[string]string{"name": "app", "branch": "main"}
	s.Env = map[string]string{"skipBuild": "true", "nope": "yes"}
	s.Args = map[string]string{"env": "prod"}
	s.AddOutput("拉取代码", "v", "abc123")
	return s
}

func TestInterpolate(t *testing.T) {
	s := newTestSet()
	cases := []struct{ in, want string }{
		{"${system.name}", "app"},
		{"deploy ${system.name} to ${args.env}", "deploy app to prod"},
		{"${output.拉取代码.v}", "abc123"}, // 节点名允许中文
		{"no refs here", "no refs here"},
		{"", ""},
	}
	for _, c := range cases {
		got, err := s.Interpolate(c.in, nil)
		if err != nil || got != c.want {
			t.Errorf("Interpolate(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

func TestInterpolateUndefinedRef(t *testing.T) {
	s := newTestSet()
	if _, err := s.Interpolate("${env.typo}", nil); err == nil {
		t.Fatal("未定义引用必须报错，不能静默置空")
	}
	if _, err := s.Interpolate("${output.不存在.x}", nil); err == nil {
		t.Fatal("未定义的 output 引用必须报错")
	}
	if _, err := s.Interpolate("${badprefix.x}", nil); err == nil {
		t.Fatal("未知前缀必须报错")
	}
}

func TestInterpolateExtra(t *testing.T) {
	s := newTestSet()
	// extra（组件结果变量）优先于四套参数；裸名只在 extra 里找
	got, err := s.Interpolate("v=${stdout}", map[string]string{"stdout": "hello"})
	if err != nil || got != "v=hello" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestInterpolateParams(t *testing.T) {
	s := newTestSet()
	in := map[string]any{"sh": "echo ${system.name}", "n": 42, "branch": []any{"main"}}
	out, err := s.InterpolateParams(in)
	if err != nil {
		t.Fatal(err)
	}
	if out["sh"] != "echo app" || out["n"] != 42 {
		t.Errorf("got %#v", out)
	}
	if _, ok := out["branch"].([]any); !ok {
		t.Error("非 string 值必须原样透传")
	}
}

func TestSkipTrue(t *testing.T) {
	s := newTestSet()
	cases := []struct {
		raw     string
		want    bool
		wantErr bool
	}{
		{"", false, false},
		{"true", true, false},             // 字面量
		{"${env.skipBuild}", true, false}, // 插值 = "true"
		{"${env.nope}", false, false},     // "yes" 不等于 "true" → 不跳过
		{"${env.typo}", false, true},      // 未定义引用 → 错误
	}
	for _, c := range cases {
		got, err := s.SkipTrue(c.raw)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("SkipTrue(%q) = %v, %v; want %v, err=%v", c.raw, got, err, c.want, c.wantErr)
		}
	}
}

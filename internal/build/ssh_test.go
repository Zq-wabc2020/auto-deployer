package build

import (
	"strings"
	"testing"
)

func TestHTTPSToSSH(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"https://github.com/user/repo.git", "git@github.com:user/repo.git"},
		{"https://github.com/user/repo", "git@github.com:user/repo"},
		{"http://github.com/user/repo.git", "git@github.com:user/repo.git"},
		{"git@github.com:user/repo.git", "git@github.com:user/repo.git"},
		{"ssh://git@github.com/user/repo.git", "ssh://git@github.com/user/repo.git"},
		{"https://gitee.com/user/repo.git", "git@gitee.com:user/repo.git"},
		{"/tmp/local-repo.git", "/tmp/local-repo.git"},
	}
	for _, tt := range tests {
		result := HTTPSToSSH(tt.input)
		if result != tt.expected {
			t.Errorf("HTTPSToSSH(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestSSHCommand(t *testing.T) {
	cmd := SSHCommand("/path/to/key")
	expected := "ssh -i /path/to/key -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
	if cmd != expected {
		t.Errorf("expected %q, got %q", expected, cmd)
	}
}

func TestSSHKeyDir(t *testing.T) {
	dir := SSHKeyDir()
	if !strings.HasSuffix(dir, ".ssh") {
		t.Errorf("expected .ssh path, got %q", dir)
	}
}

func TestEnsureSSHKey_UsesExisting(t *testing.T) {
	// Make sure we don't regenerate if key exists
	_, pubPath, pubKey, err := EnsureSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	if pubKey == "" {
		t.Error("expected non-empty public key")
	}
	if !strings.HasSuffix(pubPath, ".pub") {
		t.Errorf("expected .pub suffix, got %q", pubPath)
	}
}

func TestIsSSHAuthError(t *testing.T) {
	cases := []struct {
		input    string
		expected bool
	}{
		{"permission denied (publickey)", true},
		{"Could not read from remote repository", true},
		{"Host key verification failed", true},
		{"authentications that can continue: publickey", true},
		{"fatal: unable to access", false},
		{"exit status 1", false},
		{"", false},
	}
	for _, tc := range cases {
		// Test with a constructed error
		testErr := error(nil)
		if tc.input != "" {
			testErr = &testErrWrapper{msg: tc.input}
		}
		result := IsSSHAuthError(testErr)
		if result != tc.expected {
			t.Errorf("IsSSHAuthError(%q) = %v, want %v", tc.input, result, tc.expected)
		}
	}
}

type testErrWrapper struct {
	msg string
}

func (e *testErrWrapper) Error() string { return e.msg }

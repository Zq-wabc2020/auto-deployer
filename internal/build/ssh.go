package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DefaultKeyPaths lists the SSH private key names to check by default.
var DefaultKeyPaths = []string{
	"id_ed25519",
	"id_rsa",
	"id_ecdsa",
	"id_dsa",
}

// SSHKeyDir returns the SSH key directory path.
func SSHKeyDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ssh")
}

// EnsureSSHKey checks if a usable SSH private key exists.
// If not, it generates an ed25519 key pair at ~/.ssh/id_ed25519.
// Returns the private key path, public key path, and public key content.
func EnsureSSHKey() (privKeyPath, pubKeyPath, pubKeyContent string, err error) {
	keyDir := SSHKeyDir()
	if err := os.MkdirAll(keyDir, 0700); err != nil {
		return "", "", "", fmt.Errorf("failed to create .ssh directory: %w", err)
	}

	// Check if any default key already exists
	for _, name := range DefaultKeyPaths {
		privPath := filepath.Join(keyDir, name)
		pubPath := privPath + ".pub"
		if _, err := os.Stat(privPath); err == nil {
			if _, err := os.Stat(pubPath); err == nil {
				content, _ := os.ReadFile(pubPath)
				return privPath, pubPath, strings.TrimSpace(string(content)), nil
			}
		}
	}

	// Generate a new ed25519 key using ssh-keygen
	privPath := filepath.Join(keyDir, "id_ed25519")
	pubPath := privPath + ".pub"

	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-f", privPath, "-N", "", "-C", "deployd@auto-generated")
	cmd.Stdin = strings.NewReader("")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", "", "", fmt.Errorf("failed to generate SSH key: %w", err)
	}

	content, err := os.ReadFile(pubPath)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to read public key: %w", err)
	}

	return privPath, pubPath, strings.TrimSpace(string(content)), nil
}

// HTTPSToSSH converts an HTTPS git URL to an SSH URL.
func HTTPSToSSH(url string) string {
	if strings.HasPrefix(url, "git@") || strings.HasPrefix(url, "ssh://") {
		return url
	}
	// Only convert actual remote URLs (contain ://), skip local file paths
	if !strings.Contains(url, "://") {
		return url
	}
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	parts := strings.SplitN(url, "/", 2)
	if len(parts) != 2 {
		return url
	}
	return "git@" + parts[0] + ":" + parts[1]
}

// SSHCommand returns the GIT_SSH_COMMAND value for the given key file.
func SSHCommand(keyFile string) string {
	return fmt.Sprintf("ssh -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null", keyFile)
}

// IsSSHAuthError checks if the error is caused by SSH authentication failure.
func IsSSHAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "host key verification failed") ||
		strings.Contains(msg, "could not read from remote repository") ||
		strings.Contains(msg, "authentications that can continue") ||
		strings.Contains(msg, "publickey")
}

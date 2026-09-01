package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// FindJavaHome finds the JDK home for a given version, trying in order:
//  1. jenv (`jenv prefix <version>`) -- works on any OS where jenv is installed
//  2. /usr/libexec/java_home -v <version> (macOS)
//  3. a scan of /usr/lib/jvm/* (Debian/Ubuntu convention, e.g.
//     /usr/lib/jvm/java-8-openjdk-amd64) -- so .java-version also works on
//     Linux servers without jenv
//
// Returns "" when no matching JDK is found.
func FindJavaHome(version string) string {
	// Try jenv
	if jenvPath, err := exec.Command("jenv", "prefix", version).Output(); err == nil {
		return strings.TrimSpace(string(jenvPath))
	}
	// Try macOS java_home
	if out, err := exec.Command("/usr/libexec/java_home", "-v", version).Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	// Scan /usr/lib/jvm (Linux)
	return scanJvmDir("/usr/lib/jvm", version)
}

// versionPattern extracts the leading version number from a JVM dir name like
// "java-8-openjdk-amd64", "java-1.17.0-openjdk", or "temurin-17-jdk".
var versionPattern = regexp.MustCompile(`(\d+)(?:\.(\d+))?`)

// scanJvmDir returns the JDK directory under jvmRoot whose name matches the
// requested version. Major-version matching is used ("8" matches
// java-8-openjdk-amd64; "17" matches java-17-openjdk-amd64 but not 1.17 --
// historical 1.x names only ever existed for Java <= 8).
func scanJvmDir(jvmRoot, version string) string {
	want, err := strconv.Atoi(version)
	if err != nil {
		return ""
	}
	entries, err := os.ReadDir(jvmRoot)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if m := versionPattern.FindStringSubmatch(entry.Name()); m != nil {
			major, _ := strconv.Atoi(m[1])
			if major == 1 && m[2] != "" {
				// Historical RHEL-style names: java-1.8.0-openjdk is Java 8.
				major, _ = strconv.Atoi(m[2])
			}
			if major == want {
				dir := filepath.Join(jvmRoot, entry.Name())
				if _, err := os.Stat(filepath.Join(dir, "bin", "java")); err == nil {
					return dir
				}
			}
		}
	}
	return ""
}

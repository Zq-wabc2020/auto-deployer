package build

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CopyArtifact copies files matching pattern (glob, relative to workspace) to
// destDir, creating destDir if needed. Skips *.original.jar (maven source
// jars). Shared by every plugin whose model stages built files (jvm, static).
func CopyArtifact(workspace, pattern, destDir string, out io.Writer) error {
	matches, err := filepath.Glob(filepath.Join(workspace, pattern))
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return fmt.Errorf("no artifact matching %s", pattern)
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}
	copied := 0
	for _, src := range matches {
		if strings.HasSuffix(src, ".original.jar") {
			continue
		}
		dst := filepath.Join(destDir, filepath.Base(src))
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("failed to copy %s: %w", filepath.Base(src), err)
		}
		fmt.Fprintf(out, "copied %s to %s\n", filepath.Base(src), destDir)
		copied++
	}
	if copied == 0 {
		return fmt.Errorf("no artifact copied from %s", pattern)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

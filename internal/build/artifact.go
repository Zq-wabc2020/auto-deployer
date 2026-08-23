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
		srcInfo, err := os.Stat(src)
		if err != nil {
			return err
		}
		if srcInfo.IsDir() {
			if err := copyDir(src, srcInfo.Name(), destDir); err != nil {
				return fmt.Errorf("failed to copy dir %s: %w", src, err)
			}
		} else {
			dst := filepath.Join(destDir, filepath.Base(src))
			if err := copyFile(src, dst); err != nil {
				return fmt.Errorf("failed to copy %s: %w", filepath.Base(src), err)
			}
		}
		fmt.Fprintf(out, "copied %s to %s\n", filepath.Base(src), destDir)
		copied++
	}
	if copied == 0 {
		return fmt.Errorf("no artifact copied from %s", pattern)
	}
	return nil
}

func copyDir(oriPath, oriName, dest string) error {
	dst := filepath.Join(dest, oriName)
	err := os.MkdirAll(dst, 0755)
	if err != nil {
		return err
	}

	fileList, err := os.ReadDir(oriPath)

	if err != nil {
		return err
	}

	for _, item := range fileList {
		srcPath := filepath.Join(oriPath, item.Name())
		destPath := filepath.Join(dst, filepath.Base(srcPath))

		var err error
		if item.IsDir() {
			err = copyDir(srcPath, item.Name(), dst)
		} else {
			err = copyFile(srcPath, destPath)
		}

		if err != nil {
			return err
		}
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

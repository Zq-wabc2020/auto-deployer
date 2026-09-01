package build

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CopyArtifact copies files matching pattern (glob, relative to workspace) to
// destDir, creating destDir if needed, and returns the basenames of the copied
// entries. Skips *.original.jar (maven source jars). Shared by every plugin
// whose model stages built files (jvm, static).
//
// Directory semantics (Go's filepath.Glob has no "**" support):
//   - literal directory pattern (no glob metacharacters, e.g. "dist" or
//     "dist/"): the directory's CONTENTS are copied into destDir
//   - wildcard pattern (e.g. "dist/*") matching a directory: it is copied as
//     destDir/<name>/ including its subtree
func CopyArtifact(workspace, pattern, destDir string, out io.Writer) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(workspace, pattern))
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no artifact matching %s", pattern)
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, err
	}
	copyContents := !hasGlobMeta(pattern)
	var copied []string
	for _, src := range matches {
		info, err := os.Stat(src)
		if err != nil {
			return copied, fmt.Errorf("failed to stat %s: %w", src, err)
		}
		if info.IsDir() {
			dirDst := destDir
			name := filepath.Base(src)
			if !copyContents {
				// Wildcard match: keep the directory's own name in dest.
				dirDst = filepath.Join(destDir, name)
				copied = append(copied, name)
			}
			names, err := copyDirContents(src, dirDst, out)
			if err != nil {
				return copied, fmt.Errorf("failed to copy directory %s: %w", name, err)
			}
			if copyContents {
				copied = append(copied, names...)
			}
			continue
		}
		if strings.HasSuffix(src, ".original.jar") {
			continue
		}
		dst := filepath.Join(destDir, filepath.Base(src))
		if err := copyFile(src, dst); err != nil {
			return copied, fmt.Errorf("failed to copy %s: %w", filepath.Base(src), err)
		}
		fmt.Fprintf(out, "copied %s to %s\n", filepath.Base(src), destDir)
		copied = append(copied, filepath.Base(src))
	}
	if len(copied) == 0 {
		return copied, fmt.Errorf("no artifact copied from %s", pattern)
	}
	return copied, nil
}

func hasGlobMeta(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

// copyDirContents recursively copies the contents of src into dst and returns
// the names of the top-level entries copied (children of src).
func copyDirContents(src, dst string, out io.Writer) ([]string, error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dst, 0755); err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		s := filepath.Join(src, entry.Name())
		d := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if _, err := copyDirContents(s, d, out); err != nil {
				return names, err
			}
		} else {
			if err := copyFile(s, d); err != nil {
				return names, err
			}
		}
		names = append(names, entry.Name())
	}
	fmt.Fprintf(out, "copied contents of %s to %s\n", src, dst)
	return names, nil
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

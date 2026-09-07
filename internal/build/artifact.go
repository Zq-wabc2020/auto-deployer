package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CopyArtifact copies files matching pattern (glob or list of globs, relative to
// workspace) to destDir, creating destDir if needed, and returns the basenames of
// the copied entries. Skips *.original.jar (maven source jars). Shared by every
// plugin whose model stages built files (jvm, static, node).
//
// pattern 接受单字符串（向后兼容）或 []string（多产物，如 ["hello1.txt","test.json"]）；
// 列表每项独立解析，文件平铺到 destDir 根，目录按既有语义。ctx 用于在文件之间响应取消。
//
// Directory semantics (Go's filepath.Glob has no "**" support):
//   - literal directory pattern (no glob metacharacters, e.g. "dist" or
//     "dist/"): the directory's CONTENTS are copied into destDir
//   - wildcard pattern (e.g. "dist/*") matching a directory: it is copied as
//     destDir/<name>/ including its subtree
func CopyArtifact(ctx context.Context, workspace string, pattern interface{}, destDir string, out io.Writer) ([]string, error) {
	patterns := normalizePatterns(pattern)
	if len(patterns) == 0 {
		return nil, fmt.Errorf("empty artifact pattern")
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, err
	}
	var all []string
	for _, p := range patterns {
		// 每个 pattern 之间检查取消，避免长拷贝期间无法中断
		if err := ctx.Err(); err != nil {
			return all, err
		}
		copied, err := copyOnePattern(ctx, workspace, p, destDir, out)
		all = append(all, copied...)
		if err != nil {
			return all, err
		}
	}
	if len(all) == 0 {
		return all, fmt.Errorf("no artifact copied")
	}
	return all, nil
}

// normalizePatterns 把 pattern 归一化为 []string：单串 → 单元素切片；[]string 原样；其他 → nil。
func normalizePatterns(pattern interface{}) []string {
	switch v := pattern.(type) {
	case string:
		return []string{v}
	case []string:
		return v
	}
	return nil
}

// copyOnePattern 是原 CopyArtifact 的单 pattern 逻辑（提取出来），在文件之间检查 ctx 取消。
func copyOnePattern(ctx context.Context, workspace, pattern, destDir string, out io.Writer) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(workspace, pattern))
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no artifact matching %s", pattern)
	}
	copyContents := !hasGlobMeta(pattern)
	var copied []string
	for _, src := range matches {
		if err := ctx.Err(); err != nil {
			return copied, err
		}
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
			names, err := copyDirContents(ctx, src, dirDst, out)
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
// the names of the top-level entries copied (children of src). 在每个条目之间检查 ctx 取消。
func copyDirContents(ctx context.Context, src, dst string, out io.Writer) ([]string, error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dst, 0755); err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return names, err
		}
		s := filepath.Join(src, entry.Name())
		d := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if _, err := copyDirContents(ctx, s, d, out); err != nil {
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

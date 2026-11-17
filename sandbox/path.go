package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Resolve 把工具拿到的路径收进沙箱。
// write 为 true 时只允许工作区和 AllowedPaths；只读路径不能写。
// 沙箱关闭时原样返回，保持加固前的路径行为。
func (s *Sandbox) Resolve(path string, write bool) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("路径为空")
	}
	if s == nil || !s.enabled {
		return path, nil
	}
	if strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("%w: 路径 %s", ErrRefused, path)
	}

	expanded, err := s.expandHome(path)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(s.workspace, expanded)
	}
	expanded = filepath.Clean(expanded)

	resolved, err := walkExisting(expanded, 0)
	if err != nil {
		return "", fmt.Errorf("%w: 路径 %s: %v", ErrRefused, path, err)
	}
	if !s.allows(resolved, write) {
		return "", fmt.Errorf("%w: 路径 %s", ErrRefused, path)
	}
	return resolved, nil
}

func (s *Sandbox) expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		if strings.HasPrefix(path, "~") {
			return "", fmt.Errorf("%w: 不展开其他用户的家目录 %s", ErrRefused, path)
		}
		return path, nil
	}
	rest := strings.TrimPrefix(path, "~")
	return filepath.Join(s.workspace, rest), nil
}

func (s *Sandbox) allows(path string, write bool) bool {
	if within(path, s.workspace) {
		return true
	}
	for _, root := range s.allowed {
		if within(path, root) {
			return true
		}
	}
	if write {
		return false
	}
	for _, root := range s.readOnly {
		if within(path, root) {
			return true
		}
	}
	return false
}

func within(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// walkExisting 解析已经存在的符号链接。末尾还不存在的组件按词法拼接，
// 避免 file_write 新建文件时把父目录的链接当成可逃逸路径。
func walkExisting(path string, depth int) (string, error) {
	if depth > 16 {
		return "", fmt.Errorf("符号链接层数过多")
	}
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("需要绝对路径")
	}

	parts := strings.Split(strings.TrimPrefix(cleaned, string(os.PathSeparator)), string(os.PathSeparator))
	cur := string(os.PathSeparator)
	for i, part := range parts {
		if part == "" || part == "." {
			continue
		}
		next := filepath.Join(cur, part)
		info, err := os.Lstat(next)
		if err != nil {
			if os.IsNotExist(err) {
				rest := append([]string{cur}, parts[i:]...)
				return filepath.Clean(filepath.Join(rest...)), nil
			}
			return "", err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			cur = next
			continue
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(cur, target)
		}
		if i+1 < len(parts) {
			target = filepath.Join(append([]string{target}, parts[i+1:]...)...)
		}
		return walkExisting(filepath.Clean(target), depth+1)
	}
	return cur, nil
}

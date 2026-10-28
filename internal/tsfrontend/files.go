package tsfrontend

import (
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/totoer/goya/internal/tsfrontend/upstream/vfs"
)

// sourceFS is a frozen, read-only virtual filesystem. It has no OS fallback.
type sourceFS struct {
	files map[string]string
	dirs  map[string]vfs.Entries
}

var _ vfs.FS = (*sourceFS)(nil)

func canonicalName(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "\x00\\") || strings.Contains(name, ":") {
		return "", fmt.Errorf("invalid virtual source name %q", name)
	}
	return path.Clean("/" + strings.TrimPrefix(name, "/")), nil
}

func freezeFiles(files map[string]string) (*sourceFS, error) {
	result := &sourceFS{files: make(map[string]string, len(files)), dirs: map[string]vfs.Entries{"/": {}}}
	for name, text := range files {
		canonical, err := canonicalName(name)
		if err != nil {
			return nil, err
		}
		if _, exists := result.files[canonical]; exists {
			return nil, fmt.Errorf("duplicate canonical source name %q", canonical)
		}
		if canonical == "/" {
			return nil, fmt.Errorf("source name is a directory: %q", name)
		}
		result.files[canonical] = text
		dir := path.Dir(canonical)
		entries := result.dirs[dir]
		entries.Files = append(entries.Files, path.Base(canonical))
		result.dirs[dir] = entries
		for dir != "/" {
			parent := path.Dir(dir)
			entries := result.dirs[parent]
			if !slices.Contains(entries.Directories, path.Base(dir)) {
				entries.Directories = append(entries.Directories, path.Base(dir))
			}
			result.dirs[parent] = entries
			dir = parent
		}
	}
	for name, entries := range result.dirs {
		if _, exists := result.files[name]; exists {
			return nil, fmt.Errorf("virtual path is both a file and directory: %q", name)
		}
		slices.Sort(entries.Files)
		slices.Sort(entries.Directories)
		result.dirs[name] = entries
	}
	return result, nil
}

func (s *sourceFS) UseCaseSensitiveFileNames() bool { return true }

func (s *sourceFS) FileExists(name string) bool {
	_, ok := s.files[path.Clean(name)]
	return ok
}

func (s *sourceFS) ReadFile(name string) (string, bool) {
	contents, ok := s.files[path.Clean(name)]
	return contents, ok
}

func (s *sourceFS) DirectoryExists(name string) bool {
	_, ok := s.dirs[path.Clean(name)]
	return ok
}

func (s *sourceFS) GetAccessibleEntries(name string) vfs.Entries {
	entries := s.dirs[path.Clean(name)]
	return vfs.Entries{Files: slices.Clone(entries.Files), Directories: slices.Clone(entries.Directories)}
}

func (s *sourceFS) Stat(name string) vfs.FileInfo {
	name = path.Clean(name)
	if text, ok := s.files[name]; ok {
		return fileInfo{name: path.Base(name), size: int64(len(text))}
	}
	if s.DirectoryExists(name) {
		return fileInfo{name: path.Base(name), directory: true}
	}
	return nil
}

func (s *sourceFS) Realpath(name string) string { return path.Clean(name) }

func (*sourceFS) WriteFile(string, string) error             { return vfs.ErrPermission }
func (*sourceFS) AppendFile(string, string) error            { return vfs.ErrPermission }
func (*sourceFS) Remove(string) error                        { return vfs.ErrPermission }
func (*sourceFS) Chtimes(string, time.Time, time.Time) error { return vfs.ErrPermission }

func (s *sourceFS) WalkDir(root string, visit vfs.WalkDirFunc) error {
	var walk func(string) error
	walk = func(name string) error {
		info := s.Stat(name)
		if info == nil {
			return visit(name, nil, vfs.ErrNotExist)
		}
		err := visit(name, fs.FileInfoToDirEntry(info), nil)
		if err != nil {
			if err == fs.SkipDir && info.IsDir() {
				return nil
			}
			return err
		}
		if !info.IsDir() {
			return nil
		}
		entries := s.GetAccessibleEntries(name)
		names := append(entries.Files, entries.Directories...)
		slices.Sort(names)
		for _, child := range names {
			if err := walk(path.Join(name, child)); err != nil {
				return err
			}
		}
		return nil
	}
	err := walk(path.Clean(root))
	if err == fs.SkipAll || err == fs.SkipDir {
		return nil
	}
	return err
}

type fileInfo struct {
	name      string
	size      int64
	directory bool
}

func (f fileInfo) Name() string       { return f.name }
func (f fileInfo) Size() int64        { return f.size }
func (f fileInfo) ModTime() time.Time { return time.Time{} }
func (f fileInfo) IsDir() bool        { return f.directory }
func (f fileInfo) Sys() any           { return nil }
func (f fileInfo) Mode() fs.FileMode {
	if f.directory {
		return fs.ModeDir | 0555
	}
	return 0444
}

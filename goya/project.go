package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/totoer/goya"
)

// File access belongs to this Go application. The engine receives only a
// detached catalog and has no path back to the operating system filesystem.
func readProject(filename, root string) (goya.Project, error) {
	if filename == "" || filename == "-" {
		if root != "" {
			return goya.Project{}, fmt.Errorf("-project-root requires a file entry point")
		}
		source, err := readSource(filename)
		return goya.Project{EntryPoints: []string{"/stdin.ts"}, Files: map[string]string{"/stdin.ts": string(source)}}, err
	}
	entry, err := filepath.Abs(filename)
	if err != nil {
		return goya.Project{}, err
	}
	if root == "" {
		root = filepath.Dir(entry)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return goya.Project{}, err
	}
	relative, err := filepath.Rel(root, entry)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return goya.Project{}, fmt.Errorf("entry point is outside project root")
	}
	project := goya.Project{EntryPoints: []string{"/" + filepath.ToSlash(relative)}, Files: make(map[string]string)}
	err = filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			if name != root && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(name)
		if ext != ".ts" && ext != ".tsx" && ext != ".mts" && ext != ".cts" && ext != ".json" {
			return nil
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		source, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		project.Files["/"+filepath.ToSlash(rel)] = string(source)
		return nil
	})
	return project, err
}

func printDiagnostics(output io.Writer, diagnostics []goya.Diagnostic) {
	for _, d := range diagnostics {
		location := d.File
		if d.Range != nil {
			location = fmt.Sprintf("%s:%d:%d", location, d.Range.Start.Line, d.Range.Start.Column)
		}
		fmt.Fprintf(output, "%s: %s %s%d: %s\n", location, d.Severity, d.Namespace, d.Code, d.Message)
		if len(d.Related) > 0 {
			printDiagnostics(output, d.Related)
		}
	}
}

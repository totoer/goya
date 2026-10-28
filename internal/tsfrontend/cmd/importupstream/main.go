// Command importupstream imports the pinned TypeScript Go frontend without
// invoking npm, Node.js, code generators, or a network client.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const originalPrefix = "github.com/microsoft/typescript-go/internal/"
const localPrefix = "github.com/totoer/goya/internal/tsfrontend/upstream/"
const modificationNotice = "// Modified for Goya: internal import paths relocated by cmd/importupstream.\n\n"

// Keep compilation and editor services on one pinned source baseline. Command
// entry points and test tooling are deliberately not roots of the import.
var libraryRoots = []string{"compiler", "bundled", "parser", "checker", "ast", "ls", "project", "lsp"}

//go:embed patches/*.json
var sourcePatchFiles embed.FS

type sourcePatch struct {
	ID    string `json:"id"`
	Files []struct {
		Path   string `json:"path"`
		SHA256 string `json:"source_sha256"`
		Edits  []struct {
			Old string `json:"old"`
			New string `json:"new"`
		} `json:"edits"`
	} `json:"files"`
}

func adaptSource(name string, original []byte) ([]byte, error) {
	entries, err := sourcePatchFiles.ReadDir("patches")
	if err != nil {
		return nil, err
	}
	result := original
	for _, entry := range entries {
		data, err := sourcePatchFiles.ReadFile("patches/" + entry.Name())
		if err != nil {
			return nil, err
		}
		var patch sourcePatch
		if err := json.Unmarshal(data, &patch); err != nil {
			return nil, err
		}
		for _, file := range patch.Files {
			if file.Path != name {
				continue
			}
			if digest(original) != file.SHA256 {
				return nil, fmt.Errorf("patch %s: original source changed: %s", patch.ID, name)
			}
			for _, edit := range file.Edits {
				if edit.Old == "" || bytes.Count(result, []byte(edit.Old)) != 1 {
					return nil, fmt.Errorf("patch %s: edit does not match exactly once in %s", patch.ID, name)
				}
				result = bytes.Replace(result, []byte(edit.Old), []byte(edit.New), 1)
			}
			result = append([]byte("// Modified for Goya: "+patch.ID+".\n\n"), result...)
		}
	}
	return result, nil
}

type manifest struct {
	TypeScriptVersion string `json:"typescript_version"`
	SourceArchive     struct {
		SHA256 string `json:"sha256"`
	} `json:"source_archive"`
	Assets       map[string]string `json:"assets"`
	LocalPatches []struct {
		ID     string `json:"id"`
		SHA256 string `json:"sha256"`
	} `json:"local_patches"`
}

type fileRecord struct {
	SourceSHA256 string `json:"source_sha256"`
	LocalSHA256  string `json:"local_sha256"`
}

func main() {
	archive := flag.String("archive", "", "path to the pinned, compressed upstream source archive")
	manifestPath := flag.String("manifest", "internal/tsfrontend/manifest.json", "baseline manifest")
	output := flag.String("out", "internal/tsfrontend/upstream", "destination directory")
	check := flag.Bool("check", false, "verify the imported tree without writing")
	flag.Parse()
	if err := run(*archive, *manifestPath, *output, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func run(archivePath, manifestPath, output string, check bool) error {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var baseline manifest
	if err := json.Unmarshal(data, &baseline); err != nil {
		return err
	}
	if err := verifySourcePatches(baseline); err != nil {
		return err
	}
	if archivePath == "" {
		return errors.New("-archive is required; download the archive URL recorded in the manifest")
	}
	compressed, err := os.ReadFile(archivePath)
	if err != nil {
		return err
	}
	if digest(compressed) != baseline.SourceArchive.SHA256 {
		return errors.New("source archive SHA-256 does not match the pinned manifest")
	}
	source, err := readArchive(compressed)
	if err != nil {
		return err
	}
	version := source["internal/core/version.go"]
	if !bytes.Contains(version, []byte(`var version = "`+baseline.TypeScriptVersion+`"`)) {
		return errors.New("source version does not match the pinned TypeScript version")
	}
	for name, expected := range baseline.Assets {
		if digest(source[name]) != expected {
			return fmt.Errorf("asset checksum mismatch: %s", name)
		}
	}
	selected, err := dependencyClosure(source, libraryRoots)
	if err != nil {
		return err
	}
	embedded, err := embeddedAssets(source, selected)
	if err != nil {
		return err
	}
	files := make(map[string][]byte)
	lock := make(map[string]fileRecord)
	for name, original := range source {
		relative, internal := strings.CutPrefix(name, "internal/")
		if !internal {
			continue
		}
		dir := path.Dir(relative)
		included := selected[dir] && isLibraryFile(name, original)
		if embedded[name] {
			included = true
		}
		if !included {
			continue
		}
		modified, err := adaptSource(name, original)
		if err != nil {
			return err
		}
		if strings.HasSuffix(name, ".go") {
			modified, err = relocateImports(name, modified)
			if err != nil {
				return err
			}
		}
		files[relative] = modified
		lock[relative] = fileRecord{digest(original), digest(modified)}
	}
	for _, name := range []string{"LICENSE", "NOTICE.txt"} {
		if contents, ok := source[name]; ok {
			files[name] = contents
		}
	}
	lockData, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	files["files.lock.json"] = append(lockData, '\n')
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		destination := filepath.Join(output, filepath.FromSlash(name))
		if check {
			contents, err := os.ReadFile(destination)
			if err != nil {
				return err
			}
			if !bytes.Equal(contents, files[name]) {
				return fmt.Errorf("imported file differs: %s", destination)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(destination, files[name], 0644); err != nil {
			return err
		}
	}
	if err := filepath.WalkDir(output, func(name string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(output, name)
		if err != nil {
			return err
		}
		if _, ok := files[filepath.ToSlash(relative)]; !ok {
			return fmt.Errorf("unexpected file in imported tree: %s (remove it explicitly before updating)", name)
		}
		return nil
	}); err != nil {
		return err
	}
	fmt.Printf("TypeScript %s: %d packages, %d files, check=%t\n", baseline.TypeScriptVersion, len(selected), len(files), check)
	return nil
}

func verifySourcePatches(baseline manifest) error {
	entries, err := sourcePatchFiles.ReadDir("patches")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		data, err := sourcePatchFiles.ReadFile("patches/" + entry.Name())
		if err != nil {
			return err
		}
		var patch sourcePatch
		if err := json.Unmarshal(data, &patch); err != nil {
			return err
		}
		found := false
		for _, declared := range baseline.LocalPatches {
			if declared.ID == patch.ID {
				if found || declared.SHA256 != digest(data) {
					return fmt.Errorf("manifest patch checksum mismatch: %s", patch.ID)
				}
				found = true
			}
		}
		if !found {
			return fmt.Errorf("source patch is not pinned in manifest: %s", patch.ID)
		}
	}
	return nil
}

func embeddedAssets(source map[string][]byte, packages map[string]bool) (map[string]bool, error) {
	result := make(map[string]bool)
	for name, contents := range source {
		dir := path.Dir(name)
		if !packages[strings.TrimPrefix(dir, "internal/")] || !isLibraryFile(name, contents) {
			continue
		}
		for _, line := range strings.Split(string(contents), "\n") {
			patterns, ok := strings.CutPrefix(strings.TrimSpace(line), "//go:embed ")
			if !ok {
				continue
			}
			for _, pattern := range strings.Fields(patterns) {
				pattern = strings.TrimPrefix(pattern, "all:")
				if strings.HasPrefix(pattern, "\"") || strings.HasPrefix(pattern, "`") {
					var err error
					pattern, err = strconv.Unquote(pattern)
					if err != nil {
						return nil, err
					}
				}
				found := false
				for candidate := range source {
					relative, within := strings.CutPrefix(candidate, dir+"/")
					if !within {
						continue
					}
					matches, err := path.Match(pattern, relative)
					if err != nil {
						return nil, err
					}
					if matches || strings.HasPrefix(relative, pattern+"/") {
						result[candidate] = true
						found = true
					}
				}
				if !found {
					return nil, fmt.Errorf("missing embedded asset %q in %s", pattern, name)
				}
			}
		}
	}
	return result, nil
}

func readArchive(compressed []byte) (map[string][]byte, error) {
	zipped, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer zipped.Close()
	reader := tar.NewReader(zipped)
	result := make(map[string][]byte)
	var root string
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		// GitHub archives start with a global PAX record containing the commit.
		// It is archive metadata, not a member of the source tree.
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := strings.TrimSuffix(header.Name, "/")
		if strings.HasPrefix(name, "/") || path.Clean(name) != name || strings.Contains(name, "\\") {
			return nil, fmt.Errorf("unsafe archive path: %s", header.Name)
		}
		prefix, relative, _ := strings.Cut(name, "/")
		if root == "" {
			root = prefix
		}
		if root != prefix || prefix == ".." {
			return nil, fmt.Errorf("unexpected archive root: %s", header.Name)
		}
		if header.Typeflag != tar.TypeReg || (!strings.HasPrefix(relative, "internal/") && relative != "LICENSE" && relative != "NOTICE.txt") {
			continue
		}
		if _, exists := result[relative]; exists {
			return nil, fmt.Errorf("duplicate archive member: %s", relative)
		}
		contents, err := io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
		result[relative] = contents
	}
	return result, nil
}

func isLibraryFile(name string, contents []byte) bool {
	return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") &&
		!bytes.Contains(contents, []byte("//go:build ignore"))
}

func dependencyClosure(source map[string][]byte, roots []string) (map[string]bool, error) {
	selected := make(map[string]bool)
	queue := slices.Clone(roots)
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		if selected[dir] {
			continue
		}
		selected[dir] = true
		found := false
		for name, contents := range source {
			if path.Dir(name) != "internal/"+dir || !isLibraryFile(name, contents) {
				continue
			}
			found = true
			file, err := parser.ParseFile(token.NewFileSet(), name, contents, parser.ImportsOnly)
			if err != nil {
				return nil, err
			}
			for _, spec := range file.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return nil, err
				}
				if next, ok := strings.CutPrefix(importPath, originalPrefix); ok && !selected[next] {
					queue = append(queue, next)
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("missing upstream Go package: %s", dir)
		}
	}
	return selected, nil
}

func relocateImports(name string, contents []byte) ([]byte, error) {
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, name, contents, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	result := slices.Clone(contents)
	changed := false
	// Work backwards so changing a string's length cannot move earlier offsets.
	for _, spec := range slices.Backward(file.Imports) {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, err
		}
		if relative, ok := strings.CutPrefix(importPath, originalPrefix); ok {
			start, end := set.Position(spec.Path.Pos()).Offset, set.Position(spec.Path.End()).Offset
			replacement := []byte(strconv.Quote(localPrefix + relative))
			result = append(result[:start], append(replacement, result[end:]...)...)
			changed = true
		}
	}
	if !changed {
		return result, nil
	}
	return format.Source(append([]byte(modificationNotice), result...))
}

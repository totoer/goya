// Command lspparity maintains the pinned editor compatibility inventory.
// Checking the committed inventory is offline and needs no reference tools.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

type baseline struct {
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	ArchiveSHA256 string `json:"archive_sha256"`
}

type referenceItem struct {
	ID        string `json:"id"`
	Category  string `json:"category"`
	Source    string `json:"source"`
	SHA256    string `json:"source_sha256"`
	Exclusion string `json:"exclusion,omitempty"`
}

type reference struct {
	SchemaVersion int             `json:"schema_version"`
	TLS           baseline        `json:"typescript_language_server"`
	Oracle        baseline        `json:"oracle_typescript"`
	Native        baseline        `json:"native_typescript"`
	Items         []referenceItem `json:"items"`
}

type evidence struct {
	ID             string   `json:"id"`
	Status         string   `json:"status"`
	Implementation []string `json:"implementation"`
	DirectTests    []string `json:"direct_tests"`
	ProtocolTests  []string `json:"protocol_tests"`
	Reason         string   `json:"reason,omitempty"`
}

type inventory struct {
	ReferenceSHA256 string     `json:"reference_sha256"`
	Baseline        reference  `json:"baseline"`
	Items           []evidence `json:"coverage"`
}

var pins = reference{
	SchemaVersion: 1,
	TLS:           baseline{"6.0.2", "7b28f104e741feba092045550738e8549e4ccb11", "d3eea925798806bd2b9037d21742c5dea1de9a11d7d7ac2ef0f0b975fb32a5a1"},
	Oracle:        baseline{"6.0.3", "050880ce59e30b356b686bd3144efe24f875ebc8", "c26953b79c1197e02f5666a0b612d5b9707339557aa9744097e7d5719de1a117"},
	Native:        baseline{"7.0.2", "2bd066d87f5bafd315be9f40889d0a60b9e58e0b", "5ccb47dbb3f68cd0da58b71e6f445eee36a50bd3e9f9f330cc23a97b88500119"},
}

func hash(data []byte) string { d := sha256.Sum256(data); return hex.EncodeToString(d[:]) }

func main() {
	tls := flag.String("tls", "", "pinned TLS v6.0.2 source archive (generation only)")
	ts := flag.String("typescript", "", "pinned TypeScript v6.0.3 source archive (generation only)")
	ref := flag.String("reference", "testdata/lsp/reference.json", "pinned source interface index")
	out := flag.String("out", "docs/typescript-lsp-parity.json", "inventory")
	check := flag.Bool("check", false, "validate inventory offline")
	release := flag.Bool("release", false, "require passing evidence for every applicable item")
	flag.Parse()
	if err := run(*tls, *ts, *ref, *out, *check, *release); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func writeJSON(name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		return err
	}
	return os.WriteFile(name, append(data, '\n'), 0644)
}

func run(tls, ts, refPath, output string, check, release bool) error {
	if !check {
		if tls == "" || ts == "" {
			return fmt.Errorf("generation requires -tls and -typescript verified source roots")
		}
		r, err := collect(tls, ts)
		if err != nil {
			return err
		}
		if _, err := os.Stat(output); err == nil {
			return fmt.Errorf("refusing to overwrite existing coverage: %s; regenerate to a separate path and review", output)
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := writeJSON(refPath, r); err != nil {
			return err
		}
		data, err := os.ReadFile(refPath)
		if err != nil {
			return err
		}
		i := inventory{ReferenceSHA256: hash(data), Baseline: r}
		for _, item := range r.Items {
			e := evidence{ID: item.ID, Status: "pending", Implementation: []string{}, DirectTests: []string{}, ProtocolTests: []string{}}
			if item.Exclusion != "" {
				e.Status, e.Reason = "excluded", item.Exclusion
			}
			i.Items = append(i.Items, e)
		}
		if err := writeJSON(output, i); err != nil {
			return err
		}
	}
	raw, err := os.ReadFile(refPath)
	if err != nil {
		return err
	}
	var r reference
	if err := json.Unmarshal(raw, &r); err != nil {
		return err
	}
	data, err := os.ReadFile(output)
	if err != nil {
		return err
	}
	var i inventory
	if err := json.Unmarshal(data, &i); err != nil {
		return err
	}
	if err := validate(r, hash(raw), i, release, "."); err != nil {
		return err
	}
	fmt.Printf("TLS %s / TypeScript %s: %d inventory entries validated (release=%t)\n", r.TLS.Version, r.Native.Version, len(r.Items), release)
	return nil
}

func validate(r reference, digest string, i inventory, release bool, root string) error {
	if r.SchemaVersion != pins.SchemaVersion || r.TLS != pins.TLS || r.Oracle != pins.Oracle || r.Native != pins.Native {
		return fmt.Errorf("reference version/commit/archive pins changed")
	}
	if i.ReferenceSHA256 != digest {
		return fmt.Errorf("reference checksum mismatch")
	}
	a, _ := json.Marshal(r)
	b, _ := json.Marshal(i.Baseline)
	if string(a) != string(b) {
		return fmt.Errorf("inventory baseline differs from pinned reference")
	}
	expected := make(map[string]referenceItem)
	for _, item := range r.Items {
		if _, ok := expected[item.ID]; ok {
			return fmt.Errorf("duplicate reference: %s", item.ID)
		}
		if item.ID == "" || item.Source == "" || len(item.SHA256) != 64 {
			return fmt.Errorf("invalid source evidence: %s", item.ID)
		}
		expected[item.ID] = item
	}
	seen := make(map[string]bool)
	for _, item := range i.Items {
		ref, ok := expected[item.ID]
		if !ok {
			return fmt.Errorf("unknown coverage: %s", item.ID)
		}
		if seen[item.ID] {
			return fmt.Errorf("duplicate coverage: %s", item.ID)
		}
		seen[item.ID] = true
		switch item.Status {
		case "excluded":
			if ref.Exclusion == "" || item.Reason != ref.Exclusion {
				return fmt.Errorf("unjustified exclusion: %s", item.ID)
			}
		case "pending":
			if ref.Exclusion != "" {
				return fmt.Errorf("excluded item must retain its boundary: %s", item.ID)
			}
			if release {
				return fmt.Errorf("editor parity incomplete: %s", item.ID)
			}
		case "verified":
			if ref.Exclusion != "" {
				return fmt.Errorf("excluded capability advertised: %s", item.ID)
			}
			for _, links := range [][]string{item.Implementation, item.DirectTests, item.ProtocolTests} {
				if len(links) == 0 {
					return fmt.Errorf("missing implementation/direct/protocol evidence: %s", item.ID)
				}
				for _, link := range links {
					name, _, _ := strings.Cut(link, "#")
					if !filepath.IsLocal(name) {
						return fmt.Errorf("non-local evidence: %s", link)
					}
					if info, err := os.Stat(filepath.Join(root, name)); err != nil || !info.Mode().IsRegular() {
						return fmt.Errorf("missing evidence: %s", link)
					}
				}
			}
		default:
			return fmt.Errorf("invalid status %q: %s", item.Status, item.ID)
		}
	}
	for id := range expected {
		if !seen[id] {
			return fmt.Errorf("missing coverage: %s", id)
		}
	}
	return nil
}

func archiveFiles(name string, pin baseline) (map[string][]byte, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	if hash(data) != pin.ArchiveSHA256 {
		return nil, fmt.Errorf("source archive checksum mismatch: %s", name)
	}
	z, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer z.Close()
	r := tar.NewReader(z)
	files := make(map[string][]byte)
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		_, rel, ok := strings.Cut(h.Name, "/")
		if !ok || !filepath.IsLocal(rel) {
			return nil, fmt.Errorf("invalid source member: %s", h.Name)
		}
		if _, exists := files[rel]; exists {
			return nil, fmt.Errorf("duplicate source member: %s", rel)
		}
		// Retain only source interfaces, not the large upstream test corpus.
		if !strings.HasPrefix(rel, "src/") && rel != "docs/configuration.md" {
			continue
		}
		body, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		files[rel] = body
	}
	return files, nil
}

func collect(tls, ts string) (reference, error) {
	r := pins
	tlsFiles, err := archiveFiles(tls, pins.TLS)
	if err != nil {
		return r, err
	}
	tsFiles, err := archiveFiles(ts, pins.Oracle)
	if err != nil {
		return r, err
	}
	add := func(category, name, source string, raw []byte, exclusion string) {
		id := category + "/" + name
		for _, item := range r.Items {
			if item.ID == id {
				return
			}
		}
		r.Items = append(r.Items, referenceItem{id, category, source, hash(raw), exclusion})
	}
	read := func(root, name string) ([]byte, error) {
		files := tlsFiles
		if root == ts {
			files = tsFiles
		}
		value, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("source member missing: %s", name)
		}
		return value, nil
	}
	// All registered connection handlers, including nested language APIs.
	name := "src/lsp-connection.ts"
	data, err := read(tls, name)
	if err != nil {
		return r, err
	}
	for _, m := range regexp.MustCompile(`connection\.([\w.]+)\(server\.(\w+)\.bind`).FindAllStringSubmatch(string(data), -1) {
		add("handler", m[1], "tls:"+name, data, "")
	}
	for _, name := range []string{"src/commands.ts", "src/features/source-definition.ts", "src/commands/tsserverRequests.ts", "src/features/codeActions/quickFix.ts"} {
		data, err := read(tls, name)
		if err != nil {
			return r, err
		}
		for _, m := range regexp.MustCompile(`['"]((?:_typescript\.|typescript\.|\$/typescript)[A-Za-z]+)['"]`).FindAllStringSubmatch(string(data), -1) {
			exclusion := ""
			if m[1] == "_typescript.configurePlugin" || m[1] == "typescript.tsserverRequest" {
				exclusion = "Node.js tsserver/plugin infrastructure is outside TS/TSX editor parity"
			}
			add("command", m[1], "tls:"+name, data, exclusion)
		}
	}
	// Client continuations are part of editor parity but are not server commands.
	for _, entry := range []struct{ category, name, source string }{
		{"client-request", "_typescript.rename", "src/ts-protocol.ts"},
		{"client-command", "editor.action.showReferences", "src/features/code-lens/referencesCodeLens.ts"},
	} {
		raw, err := read(tls, entry.source)
		if err != nil {
			return r, err
		}
		if !strings.Contains(string(raw), "'"+entry.name+"'") {
			return r, fmt.Errorf("client continuation missing in pinned source: %s", entry.name)
		}
		add(entry.category, entry.name, "tls:"+entry.source, raw, "")
	}

	name = "src/utils/types.ts"
	data, err = read(tls, name)
	if err != nil {
		return r, err
	}
	kinds := map[string]string{"Empty": "", "QuickFix": "quickfix", "Refactor": "refactor", "Source": "source", "SourceFixAll": "source.fixAll", "SourceOrganizeImports": "source.organizeImports"}
	for _, m := range regexp.MustCompile(`public static readonly (\w+) = ([^;]+);`).FindAllStringSubmatch(string(data), -1) {
		if base := regexp.MustCompile(`^CodeActionKind\.(\w+)`).FindStringSubmatch(m[2]); len(base) == 2 {
			value, ok := kinds[base[1]]
			if !ok {
				return r, fmt.Errorf("unknown action kind base: %s", base[1])
			}
			for _, part := range regexp.MustCompile(`\.append\('([^']+)'\)`).FindAllStringSubmatch(m[2], -1) {
				value += "." + part[1]
			}
			kinds[m[1]] = value
		}
		if value := kinds[m[1]]; value != "" {
			add("action-kind", value, "tls:"+name, data, "")
		}
	}
	name = "src/features/fileConfigurationManager.ts"
	data, err = read(tls, name)
	if err != nil {
		return r, err
	}
	block := regexp.MustCompile(`(?s)const DEFAULT_TSSERVER_PREFERENCES[^=]*= \{(.*?)\n\};`).FindSubmatch(data)
	if len(block) != 2 {
		return r, fmt.Errorf("cannot locate TLS preference defaults")
	}
	for _, m := range regexp.MustCompile(`(?m)^    (\w+):`).FindAllStringSubmatch(string(block[1]), -1) {
		add("preference", m[1], "tls:"+name, data, "")
	}
	name = "docs/configuration.md"
	data, err = read(tls, name)
	if err != nil {
		return r, err
	}
	for _, m := range regexp.MustCompile(`(?m)^(?:\[language\]\.|(?:completions|diagnostics|implicitProjectConfiguration)\.)([\w.]+):`).FindAllStringSubmatch(string(data), -1) {
		key := strings.TrimSpace(strings.Split(m[0], ":")[0])
		key = strings.ReplaceAll(key, "[language]", "typescript")
		exclusion := ""
		if key == "implicitProjectConfiguration.checkJs" {
			exclusion = "JavaScript language service is outside TS/TSX editor parity"
		}
		add("configuration", key, "tls:"+name, data, exclusion)
	}

	// TLS accepts the complete TypeScript formatting interface. Its configuration
	// guide omits two supported fields, so documentation alone is not the index.
	formatSource := "src/services/types.ts"
	formatAPI, err := read(ts, formatSource)
	if err != nil {
		return r, err
	}
	interfaces := regexp.MustCompile(`(?s)export interface (?:EditorSettings|FormatCodeSettings)(?: extends EditorSettings)? \{(.*?)\n\}`).FindAllSubmatch(formatAPI, -1)
	if len(interfaces) != 2 {
		return r, fmt.Errorf("cannot locate formatting settings interfaces")
	}
	for _, body := range interfaces {
		for _, field := range regexp.MustCompile(`(?m)^\s*(?:readonly\s+)?(\w+)\?:[^\n;]+;`).FindAllStringSubmatch(string(body[1]), -1) {
			add("configuration", "typescript.format."+field[1], "typescript:"+formatSource, formatAPI, "")
		}
	}
	initBlock := strings.Split(string(data), "### `plugins` option")[0]
	for _, m := range regexp.MustCompile(`(?m)^\|\s*(\w+)\s*\|`).FindAllStringSubmatch(initBlock, -1) {
		key := m[1]
		if key == "Setting" {
			continue
		}
		exclusion := ""
		if slices.Contains([]string{"disableAutomaticTypingAcquisition", "maxTsServerMemory", "npmLocation", "plugins", "tsserver"}, key) {
			exclusion = "Node.js tsserver/plugin infrastructure is outside TS/TSX editor parity"
		}
		add("initialization", key, "tls:"+name, data, exclusion)
	}
	for _, group := range []struct{ dir, call, category string }{{"codefixes", "registerCodeFix", "quick-fix-provider"}, {"refactors", "registerRefactor", "refactor-provider"}} {
		var paths []string
		for path := range tsFiles {
			if strings.HasPrefix(path, "src/services/"+group.dir+"/") && strings.HasSuffix(path, ".ts") {
				paths = append(paths, path)
			}
		}
		slices.Sort(paths)
		for _, path := range paths {
			data := tsFiles[path]
			calls := regexp.MustCompile(`\b`+group.call+`\s*\(`).FindAllIndex(data, -1)
			file := filepath.Base(path)
			source := "typescript:src/services/" + group.dir + "/" + file
			for n := range calls {
				exclusion := ""
				if file == "disableJsDiagnostics.ts" {
					exclusion = "JavaScript language service is outside TS/TSX editor parity"
				}
				// This pinned provider emits only package-install commands,
				// with no source edits; npm/ATA is an agreed scope boundary.
				if file == "fixCannotFindModule.ts" {
					exclusion = "npm/automatic typing acquisition is outside embedded TS/TSX editor parity"
				}
				add(group.category, fmt.Sprintf("%s/%d", strings.TrimSuffix(file, ".ts"), n+1), source, data, exclusion)
			}
			if len(calls) > 0 && group.dir == "refactors" {
				for _, m := range regexp.MustCompile(`\bkind:\s*["'](refactor\.[\w.]+)["']`).FindAllStringSubmatch(string(data), -1) {
					add("refactor-kind", m[1], source, data, "")
				}
			}
		}
	}
	for _, name := range []string{"src/features/codeActions/fixAll.ts", "src/organize-imports.ts"} {
		data, err := read(tls, name)
		if err != nil {
			return r, err
		}
		for _, m := range regexp.MustCompile(`\bclass (\w+) (?:extends|implements)`).FindAllStringSubmatch(string(data), -1) {
			add("source-action-provider", m[1], "tls:"+name, data, "")
		}
		for _, m := range regexp.MustCompile(`const (\w+): OrganizeImportsCommand =`).FindAllStringSubmatch(string(data), -1) {
			add("source-action-provider", m[1], "tls:"+name, data, "")
		}
	}
	slices.SortFunc(r.Items, func(a, b referenceItem) int { return strings.Compare(a.ID, b.ID) })
	if len(r.Items) < 200 {
		return r, fmt.Errorf("unexpectedly incomplete reference extraction: %d entries", len(r.Items))
	}
	return r, nil
}

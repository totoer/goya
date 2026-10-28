// importcldr reproducibly selects CLDR JSON data from the pinned official archive.
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
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

const archiveDigest = "75b42b8d6762b053566a4b822c003aeeac3e38e3cc6f2152c0fdf5f3123475a4"
const commit = "a4b67247b446fd51b475991017a2481911210c60"

var locales = strings.Fields("en en-GB de fr ru sr sr-Latn ar ja zh zh-Hant hi es tr ko pt pt-PT it nl sv pl uk he id th")
var packages = strings.Fields("cldr-numbers-full cldr-dates-full cldr-localenames-full cldr-units-full cldr-misc-full cldr-cal-buddhist-full cldr-cal-japanese-full cldr-cal-roc-full")

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func run() error {
	archive := flag.String("archive", "", "official cldr-json 49.0.0 tar.gz")
	out := flag.String("out", "internal/cldrdata", "data directory")
	check := flag.Bool("check", false, "compare generated files without changing them")
	flag.Parse()
	input, err := os.ReadFile(*archive)
	if err != nil {
		return err
	}
	if digest(input) != archiveDigest {
		return fmt.Errorf("CLDR archive checksum mismatch")
	}
	reader, err := gzip.NewReader(bytes.NewReader(input))
	if err != nil {
		return err
	}
	defer reader.Close()
	tr := tar.NewReader(reader)
	localeData := make(map[string]map[string]any)
	supplemental := make(map[string]any)
	sources := make(map[string]string)
	var license []byte
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if header.Name == "cldr-json-49.0.0/LICENSE" {
			license, err = io.ReadAll(tr)
			if err != nil {
				return err
			}
			continue
		}
		const prefix = "cldr-json-49.0.0/cldr-json/"
		if !strings.HasPrefix(header.Name, prefix) {
			continue
		}
		name := strings.TrimPrefix(header.Name, prefix)
		if path.Clean(name) != name || strings.HasPrefix(name, "../") {
			return fmt.Errorf("unsafe archive path")
		}
		parts := strings.Split(name, "/")
		isSupplemental := len(parts) == 3 && parts[0] == "cldr-core" && parts[1] == "supplemental"
		isLocale := len(parts) == 4 && slices.Contains(packages, parts[0]) && parts[1] == "main" && slices.Contains(locales, parts[2])
		if !isSupplemental && !isLocale {
			continue
		}
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		var document map[string]any
		if err = json.Unmarshal(data, &document); err != nil {
			return err
		}
		stem := strings.TrimSuffix(path.Base(name), ".json")
		if isSupplemental {
			supplemental[stem] = document["supplemental"]
		} else {
			locale := parts[2]
			if localeData[locale] == nil {
				localeData[locale] = make(map[string]any)
			}
			if _, exists := localeData[locale][stem]; exists {
				return fmt.Errorf("duplicate CLDR document %s/%s", locale, stem)
			}
			main, ok := document["main"].(map[string]any)
			if !ok {
				return fmt.Errorf("missing locale root: %s", name)
			}
			value, exists := main[locale]
			if !exists {
				return fmt.Errorf("wrong locale root: %s", name)
			}
			localeData[locale][stem] = value
		}
		sources[name] = digest(data)
	}
	if len(license) == 0 {
		return fmt.Errorf("missing upstream license")
	}
	for _, locale := range locales {
		for _, document := range []string{"numbers", "currencies", "ca-gregorian", "listPatterns", "units"} {
			if localeData[locale][document] == nil {
				return fmt.Errorf("missing CLDR %s/%s", locale, document)
			}
		}
	}
	data, err := json.Marshal(map[string]any{"locales": localeData, "supplemental": supplemental})
	if err != nil {
		return err
	}
	var output bytes.Buffer
	writer, err := gzip.NewWriterLevel(&output, gzip.BestCompression)
	if err != nil {
		return err
	}
	if _, err = writer.Write(data); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	manifest, err := json.MarshalIndent(map[string]any{"version": "49.0.0", "commit": commit, "archive": "https://codeload.github.com/unicode-org/cldr-json/tar.gz/refs/tags/49.0.0", "archive_sha256": archiveDigest, "data_sha256": digest(output.Bytes()), "uncompressed_data_sha256": digest(data), "generator_go": runtime.Version(), "license": "Unicode-3.0", "locales": locales, "sources": sources}, "", "  ")
	if err != nil {
		return err
	}
	manifest = append(manifest, '\n')
	for name, data := range map[string][]byte{"data.json.gz": output.Bytes(), "manifest.json": manifest, "LICENSE": license} {
		file := filepath.Join(*out, name)
		if *check {
			existing, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			if !bytes.Equal(existing, data) {
				return fmt.Errorf("generated CLDR file differs: %s", file)
			}
		} else {
			if err = os.MkdirAll(*out, 0755); err != nil {
				return err
			}
			if err = os.WriteFile(file, data, 0644); err != nil {
				return err
			}
		}
	}
	fmt.Printf("CLDR 49.0.0: %d locales, %d source documents, %d compressed bytes\n", len(locales), len(sources), output.Len())
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

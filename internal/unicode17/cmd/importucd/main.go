// Command importucd regenerates the embedded Unicode category table and the
// official segmentation conformance fixtures from the pinned UCD archive.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const archiveSHA = "2066d1909b2ea93916ce092da1c0ee4808ea3ef8407c94b4f14f5b7eb263d28e"

type categoryRange struct {
	First, Last int
	Category    string
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func main() {
	archive := flag.String("archive", "", "official Unicode 17 UCD.zip")
	check := flag.Bool("check", false, "verify generated files without writing")
	flag.Parse()
	data, err := os.ReadFile(*archive)
	fail(err)
	if digest(data) != archiveSHA {
		fail(fmt.Errorf("UCD archive checksum mismatch"))
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	fail(err)
	selected := map[string][]byte{}
	for _, file := range reader.File {
		switch file.Name {
		case "UnicodeData.txt", "ReadMe.txt", "auxiliary/GraphemeBreakTest.txt", "auxiliary/WordBreakTest.txt", "auxiliary/SentenceBreakTest.txt":
			if selected[file.Name] != nil {
				fail(fmt.Errorf("duplicate UCD file"))
			}
			input, err := file.Open()
			fail(err)
			content, err := io.ReadAll(input)
			input.Close()
			fail(err)
			selected[file.Name] = content
		}
	}
	if len(selected) != 5 {
		fail(fmt.Errorf("missing UCD input"))
	}
	var ranges []categoryRange
	var pending *categoryRange
	appendRange := func(entry categoryRange) {
		if len(ranges) > 0 && ranges[len(ranges)-1].Last+1 == entry.First && ranges[len(ranges)-1].Category == entry.Category {
			ranges[len(ranges)-1].Last = entry.Last
		} else {
			ranges = append(ranges, entry)
		}
	}
	for _, line := range strings.Split(string(selected["UnicodeData.txt"]), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, ";")
		if len(fields) < 3 {
			fail(fmt.Errorf("malformed UnicodeData"))
		}
		code, err := strconv.ParseInt(fields[0], 16, 32)
		fail(err)
		entry := categoryRange{int(code), int(code), fields[2]}
		if strings.HasSuffix(fields[1], ", First>") {
			pending = &entry
			continue
		}
		if strings.HasSuffix(fields[1], ", Last>") {
			if pending == nil || pending.Category != entry.Category {
				fail(fmt.Errorf("unpaired UCD range"))
			}
			entry.First = pending.First
			pending = nil
		}
		appendRange(entry)
	}
	if pending != nil {
		fail(fmt.Errorf("unterminated UCD range"))
	}
	generated, err := json.Marshal(ranges)
	fail(err)
	generated = append(generated, '\n')
	manifest := struct {
		UnicodeVersion, ArchiveURL, ArchiveSHA256, CategoriesSHA256 string
		Sources                                                     map[string]string
	}{"17.0.0", "https://www.unicode.org/Public/17.0.0/ucd/UCD.zip", archiveSHA, digest(generated), map[string]string{}}
	for name, content := range selected {
		manifest.Sources[name] = digest(content)
	}
	metadata, err := json.MarshalIndent(manifest, "", "  ")
	fail(err)
	metadata = append(metadata, '\n')
	outputs := map[string][]byte{"internal/unicode17/categories.json": generated, "internal/unicode17/manifest.json": metadata, "internal/unicode17/ReadMe.txt": selected["ReadMe.txt"]}
	for _, family := range []string{"Grapheme", "Word", "Sentence"} {
		name := family + "BreakTest.txt"
		outputs[filepath.Join("testdata/unicode17", name)] = selected["auxiliary/"+name]
	}
	for name, content := range outputs {
		if *check {
			actual, err := os.ReadFile(name)
			fail(err)
			if !bytes.Equal(actual, content) {
				fail(fmt.Errorf("%s is not reproducible", name))
			}
		} else {
			fail(os.MkdirAll(filepath.Dir(name), 0755))
			fail(os.WriteFile(name, content, 0644))
		}
	}
	fmt.Printf("Unicode 17: %d category ranges; three official segmentation suites verified\n", len(ranges))
}

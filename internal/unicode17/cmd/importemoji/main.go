// Command importemoji pins all seven ECMAScript Unicode string properties.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

var sources = []struct{ file, sha string }{
	{"emoji-sequences.txt", "12cc8267dc33cbd11ed32bcf6fc5dc2ad9c7a77bae1bdfba2f41b1b9b3ead8dd"},
	{"emoji-zwj-sequences.txt", "5b25441daed2322b068c5e70cda522946a4f0274df864445a1965a92e5fc5cad"},
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func code(text string) rune {
	value, err := strconv.ParseInt(text, 16, 32)
	fail(err)
	if value < 0 || value > 0x10ffff {
		fail(fmt.Errorf("invalid emoji code point"))
	}
	return rune(value)
}
func main() {
	directory := flag.String("sources", "", "directory containing official Emoji 17 sequence files")
	check := flag.Bool("check", false, "verify generated files without writing")
	flag.Parse()
	properties := map[string][]string{}
	manifest := struct {
		UnicodeVersion  string
		Sources         map[string]string
		GeneratedSHA256 string
	}{"17.0.0", map[string]string{}, ""}
	for _, source := range sources {
		data, err := os.ReadFile(filepath.Join(*directory, source.file))
		fail(err)
		if digest(data) != source.sha {
			fail(fmt.Errorf("%s checksum mismatch", source.file))
		}
		manifest.Sources["https://www.unicode.org/Public/17.0.0/emoji/"+source.file] = source.sha
		for _, line := range strings.Split(string(data), "\n") {
			line, _, _ = strings.Cut(line, "#")
			if strings.TrimSpace(line) == "" {
				continue
			}
			fields := strings.Split(line, ";")
			if len(fields) < 2 {
				fail(fmt.Errorf("invalid emoji source"))
			}
			points := strings.Fields(fields[0])
			property := strings.TrimSpace(fields[1])
			if !slices.Contains([]string{"Basic_Emoji", "Emoji_Keycap_Sequence", "RGI_Emoji_Flag_Sequence", "RGI_Emoji_Tag_Sequence", "RGI_Emoji_Modifier_Sequence", "RGI_Emoji_ZWJ_Sequence"}, property) {
				fail(fmt.Errorf("unknown string property"))
			}
			if len(points) == 1 && strings.Contains(points[0], "..") {
				low, high, _ := strings.Cut(points[0], "..")
				first, last := code(low), code(high)
				if first > last {
					fail(fmt.Errorf("invalid emoji range"))
				}
				for point := first; point <= last; point++ {
					properties[property] = append(properties[property], string(point))
				}
			} else {
				runes := make([]rune, len(points))
				for i, point := range points {
					runes[i] = code(point)
				}
				properties[property] = append(properties[property], string(runes))
			}
		}
	}
	var all []string
	for _, values := range properties {
		all = append(all, values...)
	}
	properties["RGI_Emoji"] = all
	for property, values := range properties {
		slices.Sort(values)
		properties[property] = slices.Compact(values)
	}
	output, err := json.Marshal(properties)
	fail(err)
	output = append(output, '\n')
	manifest.GeneratedSHA256 = digest(output)
	metadata, err := json.MarshalIndent(manifest, "", "  ")
	fail(err)
	metadata = append(metadata, '\n')
	for name, content := range map[string][]byte{"internal/unicode17/emoji.json": output, "internal/unicode17/emoji-manifest.json": metadata} {
		if *check {
			actual, err := os.ReadFile(name)
			fail(err)
			if !bytes.Equal(actual, content) {
				fail(fmt.Errorf("%s is not reproducible", name))
			}
		} else {
			fail(os.WriteFile(name, content, 0644))
		}
	}
	fmt.Printf("Emoji 17: %d properties, %d RGI sequences verified\n", len(properties), len(properties["RGI_Emoji"]))
}

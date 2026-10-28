// importtz selects TZif assets from the checksum-verified PSF tzdata wheel.
// No Python, zic, C compiler, operating-system timezone database or network is
// used to import, verify, compile or execute the Go interpreter.
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
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const wheelDigest = "b683bd1b6659ddcd810ff02ad09ba821d4bf1065072805063eb35c49617905ac"
const wheelURL = "https://files.pythonhosted.org/packages/94/21/1e5995a1c920cce14e4bffae20c665ec10e7ed03ab25e006cd741092b718/tzdata-2026.5-py2.py3-none-any.whl"

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func run() error {
	wheel := flag.String("wheel", "", "official tzdata 2026.5 wheel")
	out := flag.String("out", "internal/tzdata", "data directory")
	check := flag.Bool("check", false, "compare generated files without modifying them")
	flag.Parse()
	input, err := os.ReadFile(*wheel)
	if err != nil {
		return err
	}
	if digest(input) != wheelDigest {
		return fmt.Errorf("timezone wheel checksum mismatch")
	}
	reader, err := zip.NewReader(bytes.NewReader(input), int64(len(input)))
	if err != nil {
		return err
	}
	files := make(map[string][]byte)
	sources := make(map[string]string)
	output := make(map[string][]byte)
	var rules, zoneTab []byte
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		name := file.Name
		if path.Clean(name) != name || strings.HasPrefix(name, "../") {
			return fmt.Errorf("unsafe wheel path")
		}
		read, err := file.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(read)
		_ = read.Close()
		if err != nil {
			return err
		}
		if strings.HasPrefix(name, "tzdata/zoneinfo/") {
			short := strings.TrimPrefix(name, "tzdata/zoneinfo/")
			if bytes.HasPrefix(data, []byte("TZif")) {
				if files[short] != nil {
					return fmt.Errorf("duplicate timezone")
				}
				files[short] = data
				sources[name] = digest(data)
			}
			switch short {
			case "tzdata.zi":
				rules = data
				sources[name] = digest(data)
			case "zone.tab":
				zoneTab = data
				sources[name] = digest(data)
			}
		}
		if strings.HasSuffix(name, "/licenses/LICENSE") {
			output["LICENSE"] = data
		}
		if strings.HasSuffix(name, "/licenses/LICENSE_APACHE") {
			output["LICENSE_APACHE"] = data
		}
	}
	if !bytes.HasPrefix(rules, []byte("# version 2026e\n")) || len(zoneTab) == 0 || len(output["LICENSE"]) == 0 || len(output["LICENSE_APACHE"]) == 0 {
		return fmt.Errorf("incomplete timezone source/version/licenses")
	}
	links := make(map[string]string)
	for _, line := range strings.Split(string(rules), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "L" {
			links[fields[2]] = fields[1]
		}
	}
	regions := make(map[string][]string)
	primary := make(map[string]bool)
	for _, line := range strings.Split(string(zoneTab), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		name := fields[2]
		if files[name] == nil {
			return fmt.Errorf("zone.tab name missing TZif: %s", name)
		}
		primary[name] = true
		for _, region := range strings.Split(fields[0], ",") {
			regions[region] = append(regions[region], name)
		}
	}
	aliases := make(map[string]string)
	for name := range files {
		canonical := name
		seen := make(map[string]bool)
		for !primary[canonical] && links[canonical] != "" {
			if seen[canonical] {
				return fmt.Errorf("cyclic timezone links")
			}
			seen[canonical] = true
			canonical = links[canonical]
		}
		if canonical == "Etc/UTC" || canonical == "Etc/GMT" || canonical == "UTC" || canonical == "GMT" {
			canonical = "UTC"
		}
		if canonical != "UTC" && files[canonical] == nil {
			return fmt.Errorf("timezone target missing: %s", canonical)
		}
		aliases[name] = canonical
	}
	aliases["UTC"] = "UTC"
	aliases["GMT"] = "UTC"
	for region, names := range regions {
		sort.Strings(names)
		regions[region] = names
	}
	metadata, err := json.MarshalIndent(map[string]any{"version": "2026e", "aliases": aliases, "regions": regions}, "", "  ")
	if err != nil {
		return err
	}
	metadata = append(metadata, '\n')
	output["metadata.json"] = metadata
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range names {
		file, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			return err
		}
		if _, err = file.Write(files[name]); err != nil {
			return err
		}
	}
	if err = writer.Close(); err != nil {
		return err
	}
	output["zoneinfo.zip"] = buffer.Bytes()
	manifest, err := json.MarshalIndent(map[string]any{"iana_version": "2026e", "distribution": "Python Software Foundation tzdata 2026.5", "wheel": wheelURL, "wheel_sha256": wheelDigest, "source_release": "https://www.iana.org/time-zones/releases/2026e", "zoneinfo_sha256": digest(buffer.Bytes()), "metadata_sha256": digest(metadata), "sources": sources}, "", "  ")
	if err != nil {
		return err
	}
	output["manifest.json"] = append(manifest, '\n')
	for name, data := range output {
		file := filepath.Join(*out, name)
		if *check {
			existing, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			if !bytes.Equal(existing, data) {
				return fmt.Errorf("generated timezone file differs: %s", file)
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
	fmt.Printf("IANA 2026e: %d TZif files, %d regions, %d archive bytes\n", len(files), len(regions), buffer.Len())
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

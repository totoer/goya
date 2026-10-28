// Package tzdata loads only the embedded, pinned IANA timezone catalog.
// It has no operating-system or network fallback.
package tzdata

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed zoneinfo.zip
var archive []byte

//go:embed metadata.json
var metadata []byte

type catalog struct {
	Aliases   map[string]string   `json:"aliases"`
	Regions   map[string][]string `json:"regions"`
	files     map[string]*zip.File
	names     []string
	canonical map[string]string
}

var loadCatalog = sync.OnceValue(func() *catalog {
	c := new(catalog)
	if err := json.Unmarshal(metadata, c); err != nil {
		panic(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		panic(err)
	}
	c.files = make(map[string]*zip.File)
	c.canonical = make(map[string]string)
	for _, file := range reader.File {
		c.files[file.Name] = file
	}
	for name, canonical := range c.Aliases {
		c.canonical[strings.ToLower(name)] = canonical
		if name == canonical {
			c.names = append(c.names, name)
		}
	}
	sort.Strings(c.names)
	return c
})
var locations sync.Map

// Lookup accepts named catalog entries case-insensitively and reports the
// canonical identifier. A failed lookup performs no external resolution.
func Lookup(name string) (string, *time.Location, bool) {
	c := loadCatalog()
	canonical, ok := c.canonical[strings.ToLower(name)]
	if !ok {
		return "", nil, false
	}
	if canonical == "UTC" {
		return canonical, time.UTC, true
	}
	if cached, ok := locations.Load(canonical); ok {
		return canonical, cached.(*time.Location), true
	}
	file := c.files[canonical]
	if file == nil {
		panic("canonical timezone has no embedded data")
	}
	reader, err := file.Open()
	if err != nil {
		panic(err)
	}
	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		panic(err)
	}
	location, err := time.LoadLocationFromTZData(canonical, data)
	if err != nil {
		panic(err)
	}
	stored, _ := locations.LoadOrStore(canonical, location)
	return canonical, stored.(*time.Location), true
}
func Names() []string { return append([]string(nil), loadCatalog().names...) }
func RegionNames(region string) []string {
	return append([]string(nil), loadCatalog().Regions[region]...)
}

// EquivalentNames supplies historic aliases when locale data uses an older
// stable identifier. The canonical identifier itself is first.
func EquivalentNames(canonical string) []string {
	result := []string{canonical}
	for name, target := range loadCatalog().Aliases {
		if target == canonical && name != canonical {
			result = append(result, name)
		}
	}
	sort.Strings(result[1:])
	return result
}

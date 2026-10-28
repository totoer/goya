// Package cldrdata exposes read-only, embedded locale data. It never reads the
// operating system or resolves locale data through a network service.
package cldrdata

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"sort"
	"sync"
)

//go:embed data.json.gz
var compressed []byte

var loadData = sync.OnceValue(func() map[string]any {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		panic(err)
	}
	defer reader.Close()
	var data map[string]any
	if err := json.NewDecoder(reader).Decode(&data); err != nil {
		panic(err)
	}
	return data
})

// Node has no mutating methods and does not expose the underlying maps/slices.
type Node struct{ value any }

func Root() Node { return Node{loadData()} }
func (n Node) Get(path ...string) Node {
	for _, key := range path {
		object, _ := n.value.(map[string]any)
		n.value = object[key]
	}
	return n
}
func (n Node) String() string { value, _ := n.value.(string); return value }
func (n Node) Exists() bool   { return n.value != nil }
func (n Node) Keys() []string {
	object, _ := n.value.(map[string]any)
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func (n Node) At(index int) Node {
	array, _ := n.value.([]any)
	if index < 0 || index >= len(array) {
		return Node{}
	}
	return Node{array[index]}
}
func (n Node) Len() int { array, _ := n.value.([]any); return len(array) }

// Locale returns the resolved CLDR document for a data locale and file stem.
// Locale matching and fallback belong to the calling language implementation.
func Locale(locale, document string) Node { return Root().Get("locales", locale, document) }
func Supplemental(document string) Node   { return Root().Get("supplemental", document) }
func Locales() []string                   { return Root().Get("locales").Keys() }

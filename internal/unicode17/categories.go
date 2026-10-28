// Package unicode17 provides immutable category data from the pinned Unicode
// Character Database, independent of the Go toolchain's Unicode version.
package unicode17

import (
	_ "embed"
	"encoding/json"
	"sort"
	"sync"
)

//go:embed categories.json
var categoryJSON []byte

type categoryRange struct {
	First, Last int
	Category    string
}

var categories = sync.OnceValue(func() []categoryRange {
	var data []categoryRange
	if err := json.Unmarshal(categoryJSON, &data); err != nil {
		panic(err)
	}
	return data
})

func Category(code rune) string {
	data := categories()
	index := sort.Search(len(data), func(i int) bool { return data[i].Last >= int(code) })
	if code < 0 || index == len(data) || data[index].First > int(code) {
		return "Cn"
	}
	return data[index].Category
}
func IsWordLike(code rune) bool {
	category := Category(code)
	return category[0] == 'L' || category[0] == 'N'
}

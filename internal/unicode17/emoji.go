package unicode17

import (
	_ "embed"
	"encoding/json"
	"slices"
	"sync"
)

//go:embed emoji.json
var emojiJSON []byte
var emojiProperties = sync.OnceValue(func() map[string][]string {
	var data map[string][]string
	if err := json.Unmarshal(emojiJSON, &data); err != nil {
		panic(err)
	}
	return data
})

// EmojiStrings returns a detached copy of a Unicode binary property of strings.
func EmojiStrings(property string) ([]string, bool) {
	values, ok := emojiProperties()[property]
	return slices.Clone(values), ok
}

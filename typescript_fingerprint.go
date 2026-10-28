package goya

import (
	"crypto/sha256"
	"encoding/binary"
	hexencoding "encoding/hex"
	"hash"
	"math"
	"reflect"
	"sort"

	"github.com/totoer/goya/internal/tsfrontend"
)

// Canonical operation fingerprints contain values and concrete node types,
// never Go addresses. Raw strings preserve the IR's UTF-16 representation.
func fingerprintValue(h hash.Hash, v reflect.Value) {
	write := func(s string) {
		var size [8]byte
		binary.LittleEndian.PutUint64(size[:], uint64(len(s)))
		h.Write(size[:])
		h.Write([]byte(s))
	}
	if !v.IsValid() {
		write("nil")
		return
	}
	write(v.Type().String())
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			write("nil")
		} else {
			fingerprintValue(h, v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			write(v.Type().Field(i).Name)
			fingerprintValue(h, v.Field(i))
		}
	case reflect.Slice, reflect.Array:
		var size [8]byte
		binary.LittleEndian.PutUint64(size[:], uint64(v.Len()))
		h.Write(size[:])
		for i := 0; i < v.Len(); i++ {
			fingerprintValue(h, v.Index(i))
		}
	case reflect.String:
		write(v.String())
	case reflect.Bool:
		if v.Bool() {
			write("1")
		} else {
			write("0")
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], uint64(v.Int()))
		h.Write(b[:])
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], v.Uint())
		h.Write(b[:])
	case reflect.Float32, reflect.Float64:
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], math.Float64bits(v.Float()))
		h.Write(b[:])
	case reflect.Map:
		var size [8]byte
		binary.LittleEndian.PutUint64(size[:], uint64(v.Len()))
		h.Write(size[:])
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		for _, key := range keys {
			fingerprintValue(h, key)
			fingerprintValue(h, v.MapIndex(key))
		}
	default:
		panic("non-data value in checked execution fingerprint")
	}
}

func moduleFingerprint(module *tsfrontend.IRModule, imports map[string]tsfrontend.PreparedImport, rules []tsfrontend.PreparedImportRule) string {
	h := sha256.New()
	for _, value := range []any{TypeScriptVersion(), module.Profile(), module.ID(), module.Tree().File.Source(), module.Tree().Body, module.Tree().DeclarationList, module.Dependencies(), module.Exports(), module.IsCommonJS(), module.IsJSON(), module.HasTopLevelAwait(), module.Strict()} {
		fingerprintValue(h, reflect.ValueOf(value))
	}
	fingerprintValue(h, reflect.ValueOf(imports))
	fingerprintValue(h, reflect.ValueOf(rules))
	return hexencoding.EncodeToString(h.Sum(nil))
}

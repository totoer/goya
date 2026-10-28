package goya

import (
	"fmt"
	"io"
	"math/bits"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"weak"
)

// SharedArrayBuffer is a Go-owned shared byte store. ToValue creates a separate
// realm object in each Runtime; the storage alone is shared. Its Go operations
// are safe concurrently with independently owned runtimes. Reads/writes through
// Go are byte operations; use script Atomics for atomic integer modifications.
type SharedArrayBuffer struct{ storage *sharedArrayStorage }

type sharedArrayStorage struct {
	waitMu   sync.Mutex
	waiters  map[int][]*atomicWaiter
	words    []atomic.Uint64
	length   atomic.Int64
	maximum  int
	growable bool
}

var sharedArrayBufferType = reflect.TypeOf(SharedArrayBuffer{})

// NewSharedArrayBuffer allocates a fixed store, or a growable one when a maximum
// is supplied. It reserves the maximum storage; allocation errors are returned.
func NewSharedArrayBuffer(byteLength int, maxByteLength ...int) (buffer SharedArrayBuffer, err error) {
	if len(maxByteLength) > 1 {
		return buffer, fmt.Errorf("at most one SharedArrayBuffer maximum is permitted")
	}
	maximum := byteLength
	if len(maxByteLength) == 1 {
		maximum = maxByteLength[0]
	}
	if byteLength < 0 || maximum < byteLength || maximum > int(^uint(0)>>1)-7 {
		return buffer, fmt.Errorf("invalid SharedArrayBuffer byte lengths")
	}
	defer func() {
		if failure := recover(); failure != nil {
			if _, ok := failure.(runtime.Error); ok {
				buffer = SharedArrayBuffer{}
				err = fmt.Errorf("cannot allocate SharedArrayBuffer of %d bytes", maximum)
				return
			}
			panic(failure)
		}
	}()
	storage := &sharedArrayStorage{words: make([]atomic.Uint64, (maximum+7)/8), maximum: maximum, growable: len(maxByteLength) == 1}
	storage.length.Store(int64(byteLength))
	return SharedArrayBuffer{storage: storage}, nil
}
func (buffer SharedArrayBuffer) ByteLength() int {
	if buffer.storage == nil {
		return 0
	}
	return int(buffer.storage.length.Load())
}
func (buffer SharedArrayBuffer) MaxByteLength() int {
	if buffer.storage == nil {
		return 0
	}
	return buffer.storage.maximum
}
func (buffer SharedArrayBuffer) Growable() bool {
	return buffer.storage != nil && buffer.storage.growable
}
func (buffer SharedArrayBuffer) Grow(length int) error {
	storage := buffer.storage
	if storage == nil || !storage.growable {
		return fmt.Errorf("SharedArrayBuffer is not growable")
	}
	if length < 0 || length > storage.maximum {
		return fmt.Errorf("SharedArrayBuffer growth exceeds maximum")
	}
	for {
		previous := storage.length.Load()
		if int64(length) < previous {
			return fmt.Errorf("SharedArrayBuffer cannot shrink")
		}
		if int64(length) == previous || storage.length.CompareAndSwap(previous, int64(length)) {
			return nil
		}
	}
}
func (buffer SharedArrayBuffer) ReadAt(destination []byte, offset int64) (int, error) {
	length := int64(buffer.ByteLength())
	if offset < 0 {
		return 0, fmt.Errorf("negative shared-buffer offset")
	}
	if offset > length {
		return 0, io.EOF
	}
	count := min(len(destination), int(length-offset))
	for i := 0; i < count; i++ {
		destination[i] = byte(buffer.storage.load(int(offset)+i, 1))
	}
	if count < len(destination) {
		return count, io.EOF
	}
	return count, nil
}
func (buffer SharedArrayBuffer) WriteAt(source []byte, offset int64) (int, error) {
	length := int64(buffer.ByteLength())
	if offset < 0 {
		return 0, fmt.Errorf("negative shared-buffer offset")
	}
	if offset > length {
		return 0, io.ErrShortWrite
	}
	count := min(len(source), int(length-offset))
	for i := 0; i < count; i++ {
		buffer.storage.store(int(offset)+i, 1, uint64(source[i]))
	}
	if count < len(source) {
		return count, io.ErrShortWrite
	}
	return count, nil
}

func sharedMask(size int) uint64 {
	if size == 8 {
		return ^uint64(0)
	}
	return uint64(1)<<(size*8) - 1
}
func nativeSharedBits(value uint64, size int) uint64 {
	if nativeEndian == littleEndian || size == 1 {
		return value
	}
	return bits.ReverseBytes64(value) >> (64 - size*8)
}

// Aligned 1/2/4/8-byte elements fit in one word. Subword updates use CAS, so
// ordinary shared accesses and Atomics never mix Go atomic/non-atomic memory.
func (storage *sharedArrayStorage) load(index, size int) uint64 {
	return storage.words[index/8].Load() >> ((index & 7) * 8) & sharedMask(size)
}
func (storage *sharedArrayStorage) modify(index, size int, operation func(uint64) uint64) uint64 {
	word := &storage.words[index/8]
	shift, mask := uint((index&7)*8), sharedMask(size)
	for {
		previous := word.Load()
		value := previous >> shift & mask
		next := previous & ^(mask<<shift) | (operation(value)&mask)<<shift
		if word.CompareAndSwap(previous, next) {
			return value
		}
	}
}
func (storage *sharedArrayStorage) store(index, size int, value uint64) {
	storage.modify(index, size, func(uint64) uint64 { return value })
}
func (buffer SharedArrayBuffer) toValue(r *Runtime) Value {
	if buffer.storage == nil {
		return _null
	}
	if r.sharedBuffers == nil {
		r.sharedBuffers = make(map[weak.Pointer[sharedArrayStorage]]weak.Pointer[arrayBufferObject])
	}
	key := weak.Make(buffer.storage)
	if existing := r.sharedBuffers[key].Value(); existing != nil {
		return existing.val
	}
	object := r._newArrayBuffer(r.getSharedArrayBufferPrototype(), nil)
	object.shared = buffer.storage
	r.sharedBuffers[key] = weak.Make(object)
	// Prune dead weak identities periodically; the cache retains no stores.
	if len(r.sharedBuffers)%64 == 0 {
		for key, value := range r.sharedBuffers {
			if key.Value() == nil || value.Value() == nil {
				delete(r.sharedBuffers, key)
			}
		}
	}
	return object.val
}

func (buffer *arrayBufferObject) byteLength() int {
	if buffer.shared != nil {
		return int(buffer.shared.length.Load())
	}
	return len(buffer.data)
}
func (buffer *arrayBufferObject) sharedInteger(index, size int, order byteOrder) uint64 {
	var value uint64
	for i := 0; i < size; i++ {
		shift := i * 8
		if order == bigEndian {
			shift = (size - i - 1) * 8
		}
		value |= uint64(buffer.getUint8(index+i)) << shift
	}
	return value
}
func (buffer *arrayBufferObject) setSharedInteger(index, size int, value uint64, order byteOrder) {
	for i := 0; i < size; i++ {
		shift := i * 8
		if order == bigEndian {
			shift = (size - i - 1) * 8
		}
		buffer.setUint8(index+i, uint8(value>>shift))
	}
}
func (buffer *arrayBufferObject) variableLength() bool {
	return buffer.resizable || buffer.shared != nil && buffer.shared.growable
}
func (buffer *arrayBufferObject) readBytes(offset, count int) []byte {
	result := allocByteSlice(count)
	if buffer.shared == nil {
		copy(result, buffer.data[offset:offset+count])
		return result
	}
	for i := range result {
		result[i] = byte(buffer.shared.load(offset+i, 1))
	}
	return result
}
func (buffer *arrayBufferObject) writeBytes(offset int, data []byte) {
	if buffer.shared == nil {
		copy(buffer.data[offset:], data)
		return
	}
	for i, value := range data {
		buffer.shared.store(offset+i, 1, uint64(value))
	}
}
func (buffer *arrayBufferObject) copyBytesFrom(offset int, source *arrayBufferObject, first, count int, backwards bool) {
	if buffer.shared == nil && source.shared == nil {
		if backwards {
			copy(buffer.data[offset:offset+count], source.data[first:first+count])
			return
		}
		for i := 0; i < count; i++ {
			buffer.data[offset+i] = source.data[first+i]
		}
		return
	}
	if backwards {
		for i := count - 1; i >= 0; i-- {
			buffer.setUint8(offset+i, source.getUint8(first+i))
		}
	} else {
		for i := 0; i < count; i++ {
			buffer.setUint8(offset+i, source.getUint8(first+i))
		}
	}
}

package lsp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"strconv"
	"strings"
)

// ContextReader/ContextWriter let host transports interrupt blocked IO without
// transferring stream ownership. Ordinary IO is checked between operations;
// its host must unblock an outstanding Read/Write when ending the connection.
type ContextReader interface {
	ReadContext(context.Context, []byte) (int, error)
}
type ContextWriter interface {
	WriteContext(context.Context, []byte) (int, error)
}
type contextualReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextualReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if reader, ok := r.reader.(ContextReader); ok {
		return reader.ReadContext(r.ctx, p)
	}
	return r.reader.Read(p)
}

type frameReader struct {
	reader *bufio.Reader
	limit  int
}

func newFrameReader(ctx context.Context, in io.Reader, limit int) *frameReader {
	return &frameReader{reader: bufio.NewReaderSize(contextualReader{ctx, in}, 16<<10), limit: limit}
}
func (r *frameReader) read() ([]byte, error) {
	length := -1
	headerBytes := 0
	for {
		line, err := r.reader.ReadSlice('\n')
		headerBytes += len(line)
		if headerBytes > 16<<10 || err == bufio.ErrBufferFull {
			return nil, fmt.Errorf("LSP header exceeds 16 KiB")
		}
		if err != nil {
			if err == io.EOF && headerBytes > 0 {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
		if !bytes.HasSuffix(line, []byte("\r\n")) {
			return nil, fmt.Errorf("LSP headers require CRLF")
		}
		line = line[:len(line)-2]
		if len(line) == 0 {
			break
		}
		key, value, ok := strings.Cut(string(line), ":")
		if !ok {
			return nil, fmt.Errorf("invalid LSP header")
		}
		if strings.EqualFold(key, "Content-Length") {
			if length >= 0 {
				return nil, fmt.Errorf("duplicate Content-Length")
			}
			n, err := strconv.ParseUint(strings.TrimSpace(value), 10, 63)
			if err != nil || n > uint64(r.limit) {
				return nil, fmt.Errorf("invalid or oversized Content-Length")
			}
			length = int(n)
		}
		if strings.EqualFold(key, "Content-Type") {
			media, params, err := mime.ParseMediaType(strings.TrimSpace(value))
			charset := strings.ToLower(params["charset"])
			if err != nil || (media != "application/vscode-jsonrpc" && media != "application/json") || (charset != "" && charset != "utf-8" && charset != "utf8") {
				return nil, fmt.Errorf("unsupported LSP content type or encoding")
			}
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r.reader, data); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return data, nil
}

func writeFrame(ctx context.Context, out io.Writer, data []byte) error {
	header := []byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data)))
	for _, part := range [][]byte{header, data} {
		for len(part) > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			var n int
			var err error
			if writer, ok := out.(ContextWriter); ok {
				n, err = writer.WriteContext(ctx, part)
			} else {
				n, err = out.Write(part)
			}
			if err != nil {
				return err
			}
			if n <= 0 || n > len(part) {
				return io.ErrShortWrite
			}
			part = part[n:]
		}
	}
	return nil
}

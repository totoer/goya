// Serve a complete lifecycle transcript through application-owned memory IO.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp"
	"github.com/totoer/goya/lsp/protocol"
)

func main() {
	var input, output bytes.Buffer
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":"initialize","method":"initialize","params":{"capabilities":{}}}`,
		`{"jsonrpc":"2.0","method":"initialized","params":{}}`,
		`{"jsonrpc":"2.0","id":"shutdown","method":"shutdown","params":null}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	} {
		fmt.Fprintf(&input, "Content-Length: %d\r\n\r\n%s", len(body), body)
	}
	// Replacing these buffers with host transports needs no server executable,
	// editor extension, VM, network listener or global stdin/stdout ownership.
	err := lsp.Serve(context.Background(), &input, &output, lsp.ServerOptions{SessionOptions: lsp.SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": "export const answer=42;"}}}})
	must(err)
	reader := bufio.NewReader(&output)
	responses := 0
	for {
		body, err := readFrame(reader)
		if err == io.EOF {
			break
		}
		must(err)
		responses++
		var response struct {
			ID     string             `json:"id"`
			Result json.RawMessage    `json:"result"`
			Error  *lsp.ResponseError `json:"error"`
		}
		must(json.Unmarshal(body, &response))
		if response.Error != nil {
			panic(response.Error)
		}
		if response.ID == "initialize" {
			var result protocol.InitializeResult
			must(json.Unmarshal(response.Result, &result))
			fmt.Printf("server=%s encoding=%s hover=%t\n", result.ServerInfo.Name, result.Capabilities.PositionEncoding, result.Capabilities.HoverProvider)
		}
	}
	fmt.Printf("responses=%d\n", responses)
}

func readFrame(reader *bufio.Reader) ([]byte, error) {
	length := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if line == "\r\n" {
			break
		}
		if value, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
		}
	}
	body := make([]byte, length)
	_, err := io.ReadFull(reader, body)
	return body, err
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}

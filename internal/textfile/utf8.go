package textfile

import (
	"bufio"
	"bytes"
	"io"
	"os"
)

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

// Reader removes only an optional leading UTF-8 byte order mark.
func Reader(input io.Reader) *bufio.Reader {
	r := bufio.NewReader(input)
	if prefix, _ := r.Peek(len(utf8BOM)); bytes.Equal(prefix, utf8BOM) {
		_, _ = r.Discard(len(utf8BOM))
	}
	return r
}

// ReadFile removes only an optional leading UTF-8 byte order mark.
func ReadFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	return bytes.TrimPrefix(data, utf8BOM), err
}

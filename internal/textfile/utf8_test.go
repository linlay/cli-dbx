package textfile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

func TestUTF8ReadersRemoveOnlyOneLeadingBOM(t *testing.T) {
	const bom = "\xef\xbb\xbf"
	for _, tc := range []struct{ name, input, want string }{
		{"empty", "", ""},
		{"one byte", "a", "a"},
		{"two bytes", "ab", "ab"},
		{"partial BOM one byte", "\xef", "\xef"},
		{"partial BOM two bytes", "\xef\xbb", "\xef\xbb"},
		{"BOM only", bom, ""},
		{"leading BOM", bom + "hello", "hello"},
		{"embedded FEFF", "a" + bom + "b", "a" + bom + "b"},
		{"leading and embedded BOM", bom + "a" + bom + "b", "a" + bom + "b"},
		{"double BOM", bom + bom + "hello", bom + "hello"},
		{"BOM after whitespace", " " + bom + "hello", " " + bom + "hello"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, fragmented := range []bool{false, true} {
				var input io.Reader = strings.NewReader(tc.input)
				if fragmented {
					input = iotest.OneByteReader(input)
				}
				got, err := io.ReadAll(Reader(input))
				if err != nil || string(got) != tc.want {
					t.Errorf("Reader(fragmented=%v) = %q, %v; want %q", fragmented, got, err, tc.want)
				}
			}
			path := filepath.Join(t.TempDir(), "input.txt")
			if err := os.WriteFile(path, []byte(tc.input), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := ReadFile(path)
			if err != nil || string(got) != tc.want {
				t.Errorf("ReadFile = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestUTF8ReadersPreserveReadErrors(t *testing.T) {
	wantErr := errors.New("input failed")
	for _, input := range []string{"", "\xef\xbb", "\xef\xbb\xbfcontent"} {
		reader := io.MultiReader(strings.NewReader(input), utf8ErrorReader{err: wantErr})
		_, err := io.ReadAll(Reader(reader))
		if !errors.Is(err, wantErr) {
			t.Errorf("Reader(%q) error = %v, want %v", input, err, wantErr)
		}
	}
	if _, err := ReadFile(filepath.Join(t.TempDir(), "missing.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file error = %v", err)
	}
}

type utf8ErrorReader struct{ err error }

func (r utf8ErrorReader) Read([]byte) (int, error) { return 0, r.err }

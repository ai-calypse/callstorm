package scenario

import (
	"bytes"
	"io"
)

// newTrimReader strips a UTF-8 BOM, which Windows editors like to add and
// encoding/json refuses to parse.
func newTrimReader(b []byte) io.Reader {
	return bytes.NewReader(bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF}))
}

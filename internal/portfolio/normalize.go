package portfolio

import (
	"bytes"
	"fmt"
	"io"
)

// format identifies which of PP's two XStream serialization modes a document
// uses. id-references emit reference="N"; xpath-references emit
// reference="../foo/bar".
type format int

const (
	formatIDRefs format = iota
	formatXPathRefs
)

// detectFormat sniffs the first 1024 bytes of r to determine the format, then
// returns a new io.Reader replaying the original byte stream from position 0.
// Mirrors PP's own ClientFactory detection: presence of the literal
// `<client id=` substring in the head identifies the id-references form.
func detectFormat(r io.Reader) (format, io.Reader, error) {
	head := make([]byte, 1024)
	n, err := io.ReadFull(r, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return 0, nil, fmt.Errorf("read header: %w", err)
	}
	head = head[:n]
	f := formatXPathRefs
	if bytes.Contains(head, []byte("<client id=")) {
		f = formatIDRefs
	}
	return f, io.MultiReader(bytes.NewReader(head), r), nil
}

package portfolio

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
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

// frame is one ancestor of the element currently being walked. The index is
// 1-based and counts only same-name siblings at the same parent.
type frame struct {
	name  string
	index int
}

// canonicalPath formats a frame stack as an absolute XPath. Implicit [1] is
// omitted to match XStream's wire form.
func canonicalPath(stack []frame) string {
	var sb strings.Builder
	for _, f := range stack {
		sb.WriteByte('/')
		sb.WriteString(f.name)
		if f.index > 1 {
			sb.WriteString(fmt.Sprintf("[%d]", f.index))
		}
	}
	return sb.String()
}

// resolveXPath returns the canonical absolute path of the element ref points
// at, given the stack of ancestors of the holding element (the holding element
// is the last frame in stack). Supports relative refs (../foo, ../../foo/bar[2])
// and absolute refs (/client/foo).
func resolveXPath(ref string, stack []frame) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("XPath reference is empty")
	}
	abs := strings.HasPrefix(ref, "/")
	parts := strings.Split(strings.TrimPrefix(ref, "/"), "/")
	var cur []frame
	if !abs {
		cur = append(cur, stack...)
	}
	for _, p := range parts {
		if p == ".." {
			if len(cur) == 0 {
				return "", fmt.Errorf("XPath reference %q escapes the document root", ref)
			}
			cur = cur[:len(cur)-1]
			continue
		}
		name, idx, err := parseSegment(p)
		if err != nil {
			return "", fmt.Errorf("XPath reference %q is malformed: %w", ref, err)
		}
		cur = append(cur, frame{name: name, index: idx})
	}
	return canonicalPath(cur), nil
}

// parseSegment splits a single XPath segment like "name" or "name[3]" into
// (name, index). Implicit index is 1.
func parseSegment(seg string) (string, int, error) {
	open := strings.IndexByte(seg, '[')
	if open < 0 {
		if seg == "" {
			return "", 0, fmt.Errorf("empty path segment")
		}
		return seg, 1, nil
	}
	if !strings.HasSuffix(seg, "]") {
		return "", 0, fmt.Errorf("segment %q missing closing bracket", seg)
	}
	name := seg[:open]
	if name == "" {
		return "", 0, fmt.Errorf("segment %q has no name", seg)
	}
	idx, err := strconv.Atoi(seg[open+1 : len(seg)-1])
	if err != nil || idx < 1 {
		return "", 0, fmt.Errorf("segment %q has invalid index", seg)
	}
	return name, idx, nil
}

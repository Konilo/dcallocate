package portfolio

import (
	"bytes"
	"encoding/xml"
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

// normalize rewrites an XPath-format byte stream into an id-format byte stream
// the existing parser can consume. Two phases over a buffered token stream:
//
// Phase A: walk every StartElement, maintain a frame stack, assign each
// element a synthetic numeric id keyed by its canonical path.
//
// Phase B: re-emit every token; on each StartElement, inject id="N", and if
// the element carries reference="../foo", resolve the XPath against
// the current stack and replace the value with the target's id.
//
// The two-phase split lets phase B rewrite references whose targets are
// declared later in the document.
func normalize(r io.Reader) (io.Reader, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read input: %w", err)
	}

	pathToID, err := buildPathIndex(raw)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	enc := xml.NewEncoder(&out)
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var stack []frame
	childCounts := []map[string]int{{}}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("rewrite decode: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			childCounts[len(childCounts)-1][name]++
			idx := childCounts[len(childCounts)-1][name]
			stack = append(stack, frame{name: name, index: idx})
			childCounts = append(childCounts, map[string]int{})
			path := canonicalPath(stack)
			attrs := []xml.Attr{{Name: xml.Name{Local: "id"}, Value: strconv.Itoa(pathToID[path])}}
			for _, a := range t.Attr {
				if a.Name.Local == "reference" {
					target, err := resolveXPath(a.Value, stack)
					if err != nil {
						return nil, fmt.Errorf("reference at offset %d: %w", dec.InputOffset(), err)
					}
					id, ok := pathToID[target]
					if !ok {
						return nil, fmt.Errorf("XPath reference %q at offset %d does not resolve to any element", a.Value, dec.InputOffset())
					}
					a.Value = strconv.Itoa(id)
				}
				attrs = append(attrs, a)
			}
			t.Attr = attrs
			if err := enc.EncodeToken(t); err != nil {
				return nil, fmt.Errorf("rewrite encode: %w", err)
			}
		case xml.EndElement:
			stack = stack[:len(stack)-1]
			childCounts = childCounts[:len(childCounts)-1]
			if err := enc.EncodeToken(t); err != nil {
				return nil, fmt.Errorf("rewrite encode: %w", err)
			}
		default:
			if err := enc.EncodeToken(tok); err != nil {
				return nil, fmt.Errorf("rewrite encode: %w", err)
			}
		}
	}
	if err := enc.Flush(); err != nil {
		return nil, fmt.Errorf("rewrite flush: %w", err)
	}
	return &out, nil
}

// buildPathIndex is phase A: walk every StartElement to assign a synthetic id
// keyed by its canonical path. The result is a complete path->id map covering
// every element in the document.
func buildPathIndex(raw []byte) (map[string]int, error) {
	pathToID := map[string]int{}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var stack []frame
	childCounts := []map[string]int{{}}
	nextID := 1
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("index decode: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			childCounts[len(childCounts)-1][name]++
			idx := childCounts[len(childCounts)-1][name]
			stack = append(stack, frame{name: name, index: idx})
			childCounts = append(childCounts, map[string]int{})
			pathToID[canonicalPath(stack)] = nextID
			nextID++
		case xml.EndElement:
			stack = stack[:len(stack)-1]
			childCounts = childCounts[:len(childCounts)-1]
		}
	}
	return pathToID, nil
}

package portfolio

import (
	"io"
	"strings"
	"testing"
)

func TestNormalize_RewritesXPathRefs(t *testing.T) {
	src := `<client>
<securities><security><name>A</name></security><security><name>B</name></security></securities>
<taxonomies><taxonomy><name>T</name><root><assignments>
<assignment><investmentVehicle class="security" reference="../../../../../../securities/security[2]"/></assignment>
</assignments></root></taxonomy></taxonomies>
</client>`
	r, err := normalize(strings.NewReader(src))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	out, _ := io.ReadAll(r)
	s := string(out)
	if strings.Contains(s, `reference="../`) {
		t.Errorf("XPath reference not rewritten:\n%s", s)
	}
	if !strings.Contains(s, `<security id=`) {
		t.Errorf("security has no id attribute:\n%s", s)
	}
	if !strings.Contains(s, `reference="`) {
		t.Errorf("no numeric reference left after rewrite:\n%s", s)
	}
}

func TestNormalize_ForwardReference(t *testing.T) {
	// Reference appears BEFORE its target in document order. Phase A/B split
	// must handle this: phase A assigns ids to every element regardless of
	// declaration order; phase B rewrites refs against the complete map.
	src := `<client>
<refs><ref reference="../../targets/t[2]"/></refs>
<targets><t/><t/></targets>
</client>`
	r, err := normalize(strings.NewReader(src))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	out, _ := io.ReadAll(r)
	if strings.Contains(string(out), `reference="../`) {
		t.Errorf("forward XPath reference not rewritten:\n%s", out)
	}
}

func TestNormalize_UnresolvableReference(t *testing.T) {
	src := `<client><a reference="../../nope"/></client>`
	_, err := normalize(strings.NewReader(src))
	if err == nil {
		t.Fatalf("expected error for unresolvable reference")
	}
	if !strings.Contains(err.Error(), "does not resolve") {
		t.Errorf("error %q should mention 'does not resolve'", err)
	}
}

func TestNormalize_MalformedReference(t *testing.T) {
	src := `<client><a reference="../foo["/></client>`
	_, err := normalize(strings.NewReader(src))
	if err == nil {
		t.Fatalf("expected error for malformed reference")
	}
	if !strings.Contains(err.Error(), "malformed") {
		t.Errorf("error %q should mention 'malformed'", err)
	}
}

func TestDetectFormat_IDRefs(t *testing.T) {
	src := `<?xml version="1.0"?>` + "\n" + `<client id="1"><baseCurrency>EUR</baseCurrency></client>`
	f, r, err := detectFormat(strings.NewReader(src))
	if err != nil {
		t.Fatalf("detectFormat: %v", err)
	}
	if f != formatIDRefs {
		t.Errorf("format = %v, want formatIDRefs", f)
	}
	got, _ := io.ReadAll(r)
	if string(got) != src {
		t.Errorf("rewound reader = %q, want %q", got, src)
	}
}

func TestDetectFormat_XPathRefs(t *testing.T) {
	src := `<?xml version="1.0"?>` + "\n" + `<client><baseCurrency>EUR</baseCurrency></client>`
	f, r, err := detectFormat(strings.NewReader(src))
	if err != nil {
		t.Fatalf("detectFormat: %v", err)
	}
	if f != formatXPathRefs {
		t.Errorf("format = %v, want formatXPathRefs", f)
	}
	got, _ := io.ReadAll(r)
	if string(got) != src {
		t.Errorf("rewound reader = %q, want %q", got, src)
	}
}

func TestDetectFormat_ShortInput(t *testing.T) {
	src := `<client></client>`
	f, r, err := detectFormat(strings.NewReader(src))
	if err != nil {
		t.Fatalf("detectFormat: %v", err)
	}
	if f != formatXPathRefs {
		t.Errorf("format = %v, want formatXPathRefs", f)
	}
	got, _ := io.ReadAll(r)
	if string(got) != src {
		t.Errorf("rewound reader = %q, want %q", got, src)
	}
}

func TestResolveXPath_Cases(t *testing.T) {
	stack := []frame{{"client", 1}, {"securities", 1}, {"security", 2}}
	cases := []struct {
		name string
		ref  string
		want string
	}{
		{"implicit_first_sibling", "../security", "/client/securities/security"},
		{"explicit_third_sibling", "../security[3]", "/client/securities/security[3]"},
		{"multi_level_up", "../../accounts/account", "/client/accounts/account"},
		{"absolute_path", "/client/foo/bar", "/client/foo/bar"},
		{"explicit_second_under_root", "../../securities/security[2]", "/client/securities/security[2]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveXPath(c.ref, stack)
			if err != nil {
				t.Fatalf("resolveXPath: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestResolveXPath_Errors(t *testing.T) {
	stack := []frame{{"client", 1}}
	cases := []struct{ name, ref, wantSubstr string }{
		{"empty", "", "empty"},
		{"missing_close_bracket", "foo[2", "closing bracket"},
		{"no_name", "[2]", "no name"},
		{"non_numeric_index", "foo[bar]", "invalid index"},
		{"escapes_root", "../../foo", "escapes the document root"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := resolveXPath(c.ref, stack)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.wantSubstr)
			}
			if !strings.Contains(err.Error(), c.wantSubstr) {
				t.Errorf("error %q should contain %q", err, c.wantSubstr)
			}
		})
	}
}

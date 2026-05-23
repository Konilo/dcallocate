package portfolio

import (
	"io"
	"strings"
	"testing"
)

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

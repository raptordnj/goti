package transport

import "testing"

func TestParseContentDisposition(t *testing.T) {
	tests := []struct {
		in   string
		name string
		ok   bool
	}{
		{`attachment; filename="a b.txt"`, "a b.txt", true},
		{`attachment; filename=plain.bin`, "plain.bin", true},
		{`attachment; filename*=UTF-8''%E2%82%AC%20rates.pdf; filename="x.pdf"`, "€ rates.pdf", true},
		{`attachment; filename="x.pdf"; filename*=UTF-8''%E2%82%AC%20rates.pdf`, "€ rates.pdf", true},
		{`attachment; filename*=ISO-8859-1''%E9t%E9.txt`, "été.txt", true},
		{`inline`, "", false},
		{`attachment; filename=""`, "", false},
		{`;;;===`, "", false},
		{``, "", false},
	}
	for _, tc := range tests {
		name, ok := ParseContentDisposition(tc.in)
		if name != tc.name || ok != tc.ok {
			t.Errorf("ParseContentDisposition(%q) = (%q,%v), want (%q,%v)", tc.in, name, ok, tc.name, tc.ok)
		}
	}
}

func FuzzParseContentDisposition(f *testing.F) {
	for _, s := range []string{`attachment; filename="a.txt"`, `attachment; filename*=UTF-8''%E2%82%AC`, `inline`, ``, `filename*=`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		name, ok := ParseContentDisposition(s)
		if ok && name == "" {
			t.Fatalf("ok with empty name for %q", s)
		}
	})
}

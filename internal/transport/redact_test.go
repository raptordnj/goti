package transport

import (
	"net/url"
	"strings"
	"testing"
	"unicode"
)

func TestRedact(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://user:pw@h/p?X-Amz-Signature=abc&x=1", "https://h/p?X-Amz-Signature=REDACTED&x=1"},
		{"https://h/p?X-AMZ-SIGNATURE=abc", "https://h/p?X-AMZ-SIGNATURE=REDACTED"},
		{"https://h/p?access_token=t&api_key=k&password=p&auth=a&key=k2&ok=1", "https://h/p?access_token=REDACTED&api_key=REDACTED&password=REDACTED&auth=REDACTED&key=REDACTED&ok=1"},
		{"https://h/p?PassWord=p", "https://h/p?PassWord=REDACTED"},
		{"https://h/p?pass%77ord=secret", "https://h/p?pass%77ord=REDACTED"},
		{"https://h/p?sig=a&se=b&token=c&Policy=d&Key-Pair-Id=e&Expires=1&sp=r&Signature=s", "https://h/p?sig=REDACTED&se=REDACTED&token=REDACTED&Policy=REDACTED&Key-Pair-Id=REDACTED&Expires=REDACTED&sp=REDACTED&Signature=REDACTED"},
		{"https://h/a/b.zip", "https://h/a/b.zip"},
		{"https://h/p?x=1#password=hunter22", "https://h/p?x=1"},
		{"http://%zz", "<unparseable url>"},
	}
	for _, tc := range tests {
		if got := Redact(tc.in); got != tc.want {
			t.Errorf("Redact(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsSignedURL(t *testing.T) {
	for in, want := range map[string]bool{
		"https://h/p?sig=a&se=b":        true,
		"https://h/p?X-GOOG-Signature=": true,
		"https://h/p?page=2":            false,
		"https://h/p":                   false,
	} {
		u, err := url.Parse(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := IsSignedURL(u); got != want {
			t.Errorf("IsSignedURL(%q) = %v, want %v", in, got, want)
		}
	}
}

func FuzzRedact(f *testing.F) {
	f.Add("a=1", "hunter")
	f.Add("", "abcdef")
	f.Add("x=%", "secret")
	f.Fuzz(func(t *testing.T, noise, secret string) {
		_ = Redact(noise) // never panics on arbitrary input
		if len(secret) < 6 || strings.IndexFunc(secret, func(r rune) bool { return r < 'a' || r > 'z' }) >= 0 {
			return
		}
		if strings.Contains(noise, secret) || strings.ContainsFunc(noise, unicode.IsControl) {
			return
		}
		raw := "https://h/p?" + noise + "&password=" + secret
		if out := Redact(raw); strings.Contains(out, secret) {
			t.Fatalf("Redact(%q) = %q leaks secret", raw, out)
		}
	})
}

package transport

import (
	"net/http"
	"testing"

	"github.com/raptordnj/goti/internal/domain"
)

func TestReadValidators(t *testing.T) {
	h := http.Header{}
	h.Set("ETag", `"abc"`)
	h.Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
	v := ReadValidators(h)
	if v.ETag != `"abc"` || !v.ETagStrong || v.LastModified == "" {
		t.Fatalf("%+v", v)
	}
	h.Set("ETag", `W/"abc"`)
	if v := ReadValidators(h); v.ETagStrong || v.ETag != `W/"abc"` {
		t.Fatalf("weak: %+v", v)
	}
	if v := ReadValidators(http.Header{}); v != (domain.Validators{}) {
		t.Fatalf("empty: %+v", v)
	}
}

func TestValidateRangeResponse(t *testing.T) {
	const lm = "Wed, 21 Oct 2015 07:28:00 GMT"
	want := domain.ByteRange{Start: 100, End: 200}
	strong := domain.Validators{ETag: `"v1"`, ETagStrong: true}
	lmOnly := domain.Validators{LastModified: lm}

	mk := func(status int, cr, etag, mod string) *http.Response {
		h := http.Header{}
		if cr != "" {
			h.Set("Content-Range", cr)
		}
		if etag != "" {
			h.Set("ETag", etag)
		}
		if mod != "" {
			h.Set("Last-Modified", mod)
		}
		return &http.Response{StatusCode: status, Header: h}
	}
	cases := []struct {
		name string
		resp *http.Response
		v    domain.Validators
		tot  int64
		kind domain.ErrorKind
		ok   bool
	}{
		{"exact", mk(206, "bytes 100-199/1000", `"v1"`, ""), strong, 1000, 0, true},
		{"200", mk(200, "", `"v1"`, ""), strong, 1000, domain.KindIntegrity, false},
		{"wrong interval", mk(206, "bytes 100-198/1000", `"v1"`, ""), strong, 1000, domain.KindIntegrity, false},
		{"missing content-range", mk(206, "", `"v1"`, ""), strong, 1000, domain.KindIntegrity, false},
		{"malformed content-range", mk(206, "bytes x", `"v1"`, ""), strong, 1000, domain.KindIntegrity, false},
		{"wrong total", mk(206, "bytes 100-199/1001", `"v1"`, ""), strong, 1000, domain.KindRemoteChanged, false},
		{"etag differs", mk(206, "bytes 100-199/1000", `"v2"`, ""), strong, 1000, domain.KindRemoteChanged, false},
		{"lm equal", mk(206, "bytes 100-199/1000", "", lm), lmOnly, 1000, 0, true},
		{"lm differs", mk(206, "bytes 100-199/1000", "", "Thu, 22 Oct 2015 07:28:00 GMT"), lmOnly, 1000, domain.KindRemoteChanged, false},
		{"no stored validators", mk(206, "bytes 100-199/1000", `"zz"`, ""), domain.Validators{}, 1000, 0, true},
		{"mismatch beats interval", mk(206, "bytes 0-99/1000", `"v2"`, ""), strong, 1000, domain.KindRemoteChanged, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRangeResponse(tc.resp, want, tc.tot, tc.v)
			if tc.ok {
				if err != nil {
					t.Fatalf("unexpected %v", err)
				}
				return
			}
			if err == nil || domain.KindOf(err) != tc.kind {
				t.Fatalf("got %v (kind %v) want %v", err, domain.KindOf(err), tc.kind)
			}
		})
	}
}

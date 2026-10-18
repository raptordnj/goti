package transport

import (
	"net/url"
	"strings"
)

const redactedValue = "REDACTED"

// signedKeys reports whether k (any case) is a signed/expiring-URL query key
// (spec §4.1 step 5).
func signedKeys(k string) bool {
	switch strings.ToLower(k) {
	case "x-amz-signature", "x-amz-credential", "x-goog-signature", "signature",
		"expires", "sig", "se", "sp", "token", "policy", "key-pair-id":
		return true
	}
	return false
}

// sensitiveKeys reports whether k (any case) must have its value redacted
// (spec §11.5): the signed keys plus common credential keys.
func sensitiveKeys(k string) bool {
	if signedKeys(k) {
		return true
	}
	switch strings.ToLower(k) {
	case "access_token", "api_key", "key", "password", "auth":
		return true
	}
	return false
}

// queryKey returns the decoded form of a raw query key, or the raw key when it
// is not valid percent-encoding.
func queryKey(raw string) string {
	if k, err := url.QueryUnescape(raw); err == nil {
		return k
	}
	return raw
}

// IsSignedURL reports whether u carries signed/expiring-URL query keys.
func IsSignedURL(u *url.URL) bool {
	if u == nil {
		return false
	}
	for _, pair := range strings.Split(u.RawQuery, "&") {
		k, _, _ := strings.Cut(pair, "=")
		if signedKeys(queryKey(k)) {
			return true
		}
	}
	return false
}

// Redact returns raw with userinfo and fragment removed and the values of
// sensitive query keys replaced by REDACTED. Scheme, host, path and the order
// of query parameters are preserved. Unparseable input yields a placeholder.
func Redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable url>"
	}
	u.User = nil
	u.Fragment = ""
	u.RawFragment = ""
	if u.RawQuery != "" {
		pairs := strings.Split(u.RawQuery, "&")
		for i, pair := range pairs {
			k, _, _ := strings.Cut(pair, "=")
			if sensitiveKeys(queryKey(k)) {
				pairs[i] = k + "=" + redactedValue
			}
		}
		u.RawQuery = strings.Join(pairs, "&")
	}
	return u.String()
}

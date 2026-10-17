package transport

import (
	"net/http"
	"strings"

	"github.com/raptordnj/swiftfetch/internal/domain"
)

// ReadValidators extracts the ETag and Last-Modified validators from h.
func ReadValidators(h http.Header) domain.Validators {
	etag := strings.TrimSpace(h.Get("ETag"))
	return domain.Validators{
		ETag:         etag,
		ETagStrong:   etag != "" && !strings.HasPrefix(etag, "W/"),
		LastModified: strings.TrimSpace(h.Get("Last-Modified")),
	}
}

// ValidateRangeResponse checks a response to a ranged request for want
// against the stored total (negative when unknown) and validators. A total or
// validator mismatch is KindRemoteChanged; a non-206 status or a Content-Range
// interval mismatch is KindIntegrity. Validators are checked before the
// interval, so a changed resource is never misreported as a bad interval.
func ValidateRangeResponse(resp *http.Response, want domain.ByteRange, total int64, v domain.Validators) error {
	if resp.StatusCode != http.StatusPartialContent {
		return domain.NewError(domain.KindIntegrity, "ranged request answered with status "+http.StatusText(resp.StatusCode)+", want 206", "", nil)
	}
	cr, err := ParseContentRange(resp.Header.Get("Content-Range"))
	if err != nil || cr.Unsatisfied {
		return domain.NewError(domain.KindIntegrity, "206 response has missing or malformed Content-Range", "", err)
	}
	if total >= 0 && (!cr.TotalKnown || cr.Total != total) {
		return domain.NewError(domain.KindRemoteChanged, "resource length changed on the server", "restart the download", nil)
	}
	if v.Kind() != domain.ValidatorNone && !v.Matches(ReadValidators(resp.Header)) {
		return domain.NewError(domain.KindRemoteChanged, "resource validators changed on the server", "restart the download", nil)
	}
	if cr.Range != want {
		return domain.NewError(domain.KindIntegrity, "Content-Range does not match the requested range", "", nil)
	}
	return nil
}

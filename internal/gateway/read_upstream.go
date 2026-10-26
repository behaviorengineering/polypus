package gateway

import (
	"io"
	"strconv"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// readUpstream reads at most limit bytes of an upstream body. A longer body is an
// error rather than a truncation the client would get as a cut-off 200.
func readUpstream(r io.Reader, limit int64, op string) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeUnavailable, op, "read upstream body")
	}
	if int64(len(raw)) > limit {
		return nil, derrors.New(derrors.CodeUnavailable, op, "upstream response too large").
			With("limit_bytes", strconv.FormatInt(limit, 10))
	}
	return raw, nil
}

package cloudflare

import (
	"io"
	"strconv"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// runMaxBody caps a Workers AI /run reply (audio, transcripts, SystemOne, batch
// results; a batch input may itself be 10 MB).
const runMaxBody = 64 << 20

// readCapped reads at most limit bytes of an upstream body. A longer body is an
// error rather than a silent truncation that later fails as a parse error.
func readCapped(r io.Reader, limit int64, op string) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeUnavailable, op, "read body")
	}
	if int64(len(raw)) > limit {
		return nil, derrors.New(derrors.CodeUnavailable, op, "upstream response too large").
			With("limit_bytes", strconv.FormatInt(limit, 10))
	}
	return raw, nil
}

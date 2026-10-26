package batch

import (
	"crypto/rand"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// newID returns prefix_ and 26 random lowercase base32 characters. crypto/rand
// cannot fail since Go 1.24 (it crashes the process instead), so there is no
// fallback id to collide on.
func newID(prefix string) string {
	return prefix + "_" + strings.ToLower(rand.Text())
}

// checkID trims id and rejects anything but one path segment of letters, digits, '_'
// and '-'. Ids become file names under the store root, so a '/', '\' or ".." in a
// caller-supplied id would reach files outside it.
func checkID(op, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", derrors.New(derrors.CodeInvalid, op, "id required")
	}
	for _, r := range id {
		if !isIDRune(r) {
			return "", derrors.New(derrors.CodeInvalid, op, "invalid id")
		}
	}
	return id, nil
}

func isIDRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
}

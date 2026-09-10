package app

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

var errInvalidStoredText = errors.New("Fields must be valid text without null characters.")

// PostgreSQL text rejects NUL and invalid UTF-8. Validate before normalization
// and persistence so SQL Server cannot silently accept or repair that input.
func validStoredText(values ...string) bool {
	for _, value := range values {
		if !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
			return false
		}
	}
	return true
}

var errInvalidRecordID = errors.New("invalid record identifier")

var canonicalRecordID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// All application-generated IDs have this shape. SQL Server's UUID conversion
// can truncate a valid prefix plus extra input; reject it before any lookup/write.
func validateRecordIDs(ids ...string) error {
	for _, id := range ids {
		if !canonicalRecordID.MatchString(id) {
			return errInvalidRecordID
		}
	}
	return nil
}

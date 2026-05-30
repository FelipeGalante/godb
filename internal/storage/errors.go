package storage

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidMagic       = errors.New("storage: invalid magic bytes")
	ErrUnsupportedVersion = errors.New("storage: unsupported file format version")
	ErrPageSizeMismatch   = errors.New("storage: page size mismatch")
	ErrTruncatedFile      = errors.New("storage: file truncated")
	ErrPageOutOfRange     = errors.New("storage: page id out of range")
	ErrClosed             = errors.New("storage: pager closed")
)

// CorruptionError describes a structural problem on a specific page that
// the pager detected but could not recover from. It is returned from
// places that have a page id in hand; callers can errors.As to inspect.
type CorruptionError struct {
	PageID PageID
	Reason string
}

func (e *CorruptionError) Error() string {
	return fmt.Sprintf("storage: corruption on page %d: %s", e.PageID, e.Reason)
}

package twitchchat

import "context"

// StoredSource will serve previously captured chat messages.
//
// It is a placeholder. A real implementation must return the most recent
// messages stored for req.Channel, at most req.MaxMessages of them, ordered
// oldest first. MaxDuration does not apply to stored messages.
type StoredSource struct{}

// Fetch always returns ErrNotImplemented.
func (StoredSource) Fetch(context.Context, Request) ([]Message, error) {
	return nil, ErrNotImplemented
}

package storage

import (
	"context"
	"log/slog"
)

// Reaper removes assets that a record no longer points at.
//
// Deletion is deliberately best effort: an image that outlives its record costs
// storage, but failing a user's edit because the storage provider is briefly
// unreachable costs them their work. Every failure is logged with the public id
// so it can be reconciled later.
type Reaper struct {
	uploader Uploader
	log      *slog.Logger
}

func NewReaper(uploader Uploader, log *slog.Logger) *Reaper {
	return &Reaper{uploader: uploader, log: log}
}

// Discard removes an asset if there is one to remove.
//
// It takes a context that is NOT the request's: a delete issued as the request
// returns would be cancelled the moment the response is written.
func (r *Reaper) Discard(ctx context.Context, publicID string) {
	if r == nil || publicID == "" {
		return
	}

	if err := r.uploader.Delete(ctx, publicID); err != nil {
		r.log.Warn("could not delete replaced image; it is now orphaned",
			slog.String("public_id", publicID),
			slog.String("error", err.Error()))
	}
}

// DiscardAll removes several assets, skipping empty ids.
func (r *Reaper) DiscardAll(ctx context.Context, publicIDs []string) {
	for _, id := range publicIDs {
		r.Discard(ctx, id)
	}
}

package metrics

import (
	"context"

	"github.com/fil-forge/libforge/commands/blob"
	"github.com/fil-forge/libforge/commands/upload"
	"github.com/fil-forge/ucantone/did"
)

var BlobAddTotalMetric = blob.Add.String() + "-total"
var BlobAddSizeTotalMetric = blob.Add.String() + "-size-total"

var BlobRemoveTotalMetric = blob.Remove.String() + "-total"
var BlobRemoveSizeTotalMetric = blob.Remove.String() + "-size-total"

var UploadAddTotalMetric = upload.Add.String() + "-total"
var UploadRemoveTotalMetric = upload.Remove.String() + "-total"

type Store interface {
	// Get all metrics from storage.
	Get(ctx context.Context) (map[string]uint64, error)
	// Increment total values of the given metrics.
	IncrementTotals(ctx context.Context, inc map[string]uint64) error
}

// SpaceStore holds the running totals for a space as recorded by one of its
// storage providers. A change to a space moves the counters of every provider
// serving it, matching the space diff log, which records a row per provider.
type SpaceStore interface {
	// Get all metrics a provider has recorded for a space.
	Get(ctx context.Context, provider did.DID, space did.DID) (map[string]uint64, error)
	// Increment total values of the given metrics for a space, as recorded by
	// the given provider.
	IncrementTotals(ctx context.Context, provider did.DID, space did.DID, inc map[string]uint64) error
}

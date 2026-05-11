package metrics

import (
	"context"

	"github.com/fil-forge/libforge/capabilities/blob"
	"github.com/fil-forge/libforge/capabilities/upload"
	"github.com/fil-forge/ucantone/did"
)

const BlobAddTotalMetric = blob.AddCommand + "-total"
const BlobAddSizeTotalMetric = blob.AddCommand + "-size-total"

const BlobRemoveTotalMetric = blob.RemoveCommand + "-total"
const BlobRemoveSizeTotalMetric = blob.RemoveCommand + "-size-total"

const UploadAddTotalMetric = upload.AddCommand + "-total"
const UploadRemoveTotalMetric = upload.RemoveCommand + "-total"

type Store interface {
	// Get all metrics from storage.
	Get(ctx context.Context) (map[string]uint64, error)
	// Increment total values of the given metrics.
	IncrementTotals(ctx context.Context, inc map[string]uint64) error
}

type SpaceStore interface {
	// Get all metrics for a space from storage.
	Get(ctx context.Context, space did.DID) (map[string]uint64, error)
	// Increment total values of the given metrics for a space.
	IncrementTotals(ctx context.Context, space did.DID, inc map[string]uint64) error
}

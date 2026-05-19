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

type SpaceStore interface {
	// Get all metrics for a space from storage.
	Get(ctx context.Context, space did.DID) (map[string]uint64, error)
	// Increment total values of the given metrics for a space.
	IncrementTotals(ctx context.Context, space did.DID, inc map[string]uint64) error
}

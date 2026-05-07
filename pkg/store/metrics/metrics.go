package metrics

import (
	"context"

	"github.com/fil-forge/go-libstoracha/capabilities/space/blob"
	"github.com/fil-forge/go-libstoracha/capabilities/upload"
	"github.com/fil-forge/go-ucanto/did"
)

const BlobAddTotalMetric = blob.AddAbility + "-total"
const BlobAddSizeTotalMetric = blob.AddAbility + "-size-total"

const BlobRemoveTotalMetric = blob.RemoveAbility + "-total"
const BlobRemoveSizeTotalMetric = blob.RemoveAbility + "-size-total"

const UploadAddTotalMetric = upload.AddAbility + "-total"
const UploadRemoveTotalMetric = upload.RemoveAbility + "-total"

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

package handlers_test

import (
	"testing"

	"github.com/fil-forge/sprue/pkg/store/metrics"
	metrics_store "github.com/fil-forge/sprue/pkg/store/metrics/memory"
	upload_store "github.com/fil-forge/sprue/pkg/store/upload/memory"
	uploaddiff_store "github.com/fil-forge/sprue/pkg/store/upload_diff/memory"
)

// uploadStoreFixture is a memory upload store with the metric and diff stores
// it records object counts through, so a handler test can assert on them.
type uploadStoreFixture struct {
	*upload_store.Store
	spaceMetrics metrics.SpaceStore
	adminMetrics metrics.Store
}

func newUploadStoreFixture(t *testing.T) *uploadStoreFixture {
	t.Helper()
	spaceMetrics := metrics_store.NewSpaceStore()
	adminMetrics := metrics_store.New()
	return &uploadStoreFixture{
		Store:        upload_store.New(uploaddiff_store.New(), spaceMetrics, adminMetrics),
		spaceMetrics: spaceMetrics,
		adminMetrics: adminMetrics,
	}
}

package handlers_test

import (
	"testing"

	"github.com/fil-forge/sprue/internal/testutil"
	consumer_store "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	"github.com/fil-forge/sprue/pkg/store/metrics"
	metrics_store "github.com/fil-forge/sprue/pkg/store/metrics/memory"
	upload_store "github.com/fil-forge/sprue/pkg/store/upload/memory"
	uploaddiff_store "github.com/fil-forge/sprue/pkg/store/upload_diff/memory"
	"github.com/fil-forge/ucantone/did"
	"github.com/stretchr/testify/require"
)

// uploadStoreFixture is a memory upload store with the consumer, metric and
// diff stores it records object counts through. Recording an upload needs the
// space's provider/subscription for the diff row, so a test provisions each
// space it writes against with provision.
type uploadStoreFixture struct {
	*upload_store.Store
	consumers    *consumer_store.Store
	spaceMetrics metrics.SpaceStore
	adminMetrics metrics.Store
}

func newUploadStoreFixture(t *testing.T) *uploadStoreFixture {
	t.Helper()
	consumers := consumer_store.New()
	spaceMetrics := metrics_store.NewSpaceStore()
	adminMetrics := metrics_store.New()
	return &uploadStoreFixture{
		Store:        upload_store.New(uploaddiff_store.New(), consumers, spaceMetrics, adminMetrics),
		consumers:    consumers,
		spaceMetrics: spaceMetrics,
		adminMetrics: adminMetrics,
	}
}

// provision gives space a consumer, so uploads recorded against it have a
// provider and subscription to key their diff rows by.
func (f *uploadStoreFixture) provision(t *testing.T, space did.DID) {
	t.Helper()
	require.NoError(t, f.consumers.Add(
		t.Context(), testutil.RandomDID(t), space, testutil.RandomDID(t), "sub1", testutil.RandomCID(t),
	))
}

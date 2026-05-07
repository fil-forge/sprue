package routing

import (
	"context"
	"math/rand"
	"net/url"
	"slices"

	"github.com/fil-forge/go-libstoracha/capabilities/types"
	"github.com/fil-forge/go-libstoracha/digestutil"
	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	"github.com/fil-forge/sprue/pkg/store"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
	"go.uber.org/zap"
)

const CandidateUnavailableErrorName = "CandidateUnavailable"

// ErrCandidateUnavailable is returned when there are no candidates willing to
// allocate space for the given blob.
var ErrCandidateUnavailable = errors.New(CandidateUnavailableErrorName, "no storage providers available")

type selectCfg struct {
	exclusions []ucan.Principal
}

type SelectOption func(*selectCfg)

// WithExclusions configures a list of storage providers that should be excluded
// from the routing selection.
func WithExclusions(providers ...ucan.Principal) SelectOption {
	return func(cfg *selectCfg) {
		cfg.exclusions = append(cfg.exclusions, providers...)
	}
}

type StorageProviderInfo struct {
	ID       ucan.Principal
	Endpoint url.URL
	// FIXME: the client should authorize the upload service to blob/allocate and
	// blob/accept in the blob/add invocation. The upload service should use that
	// delegation to authorize allocate and accept calls to the storage provider.
	// This removes the need for storage providers to grant love lived delegations
	// to the upload service. We will fix this in UCAN 1.0.
	Proof delegation.Delegation
}

type Service struct {
	storageProviderStore storageprovider.Store
	logger               *zap.Logger
}

func NewService(storageProviderStore storageprovider.Store, logger *zap.Logger) *Service {
	return &Service{
		storageProviderStore: storageProviderStore,
		logger:               logger,
	}
}

// GetProviderInfo returns information about a registered storage provider. It
// may return [storageprovider.ErrStorageProviderNotFound].
func (s *Service) GetProviderInfo(ctx context.Context, provider ucan.Principal) (StorageProviderInfo, error) {
	rec, err := s.storageProviderStore.Get(ctx, provider.DID())
	if err != nil {
		return StorageProviderInfo{}, err
	}
	return StorageProviderInfo{
		ID:       rec.Provider,
		Endpoint: rec.Endpoint,
		Proof:    rec.Proof,
	}, nil
}

// SelectStorageProvider selects a candidate for blob allocation from the
// current list of available storage nodes. It may return
// [ErrCandidateUnavailable] if no candidates are available.
func (s *Service) SelectStorageProvider(ctx context.Context, blob types.Blob, options ...SelectOption) (StorageProviderInfo, error) {
	cfg := &selectCfg{}
	for _, option := range options {
		option(cfg)
	}
	log := s.logger.With(
		zap.Dict(
			"blob",
			zap.String("digest", digestutil.Format(blob.Digest)),
			zap.Uint64("size", blob.Size),
		),
	)
	log.Debug("selecting storage provider")

	candidates, err := listProviders(ctx, s.storageProviderStore)
	if err != nil {
		log.Error("failed to list storage providers", zap.Error(err))
		return StorageProviderInfo{}, err
	}

	total := len(candidates)

	candidates = filterExcludedProviders(candidates, cfg.exclusions)
	candidates = filterZeroWeightProviders(candidates)
	if len(candidates) == 0 {
		log.Warn("no candidates available after filters applied", zap.Int("total", total))
		return StorageProviderInfo{}, ErrCandidateUnavailable
	}

	var weights []uint64
	for _, c := range candidates {
		weights = append(weights, uint64(c.Weight))
	}
	selected := candidates[weightedRandomInt(weights)]
	return StorageProviderInfo{
		ID:       selected.Provider,
		Endpoint: selected.Endpoint,
		Proof:    selected.Proof,
	}, nil
}

// SelectReplicationProvider selects a candidate for blob allocation from the
// current list of available storage nodes, excluding the primary node. It may
// return [ErrCandidateUnavailable] if no candidates are available.
func (s *Service) SelectReplicationProvider(ctx context.Context, primary ucan.Principal, blob types.Blob, options ...SelectOption) (StorageProviderInfo, error) {
	cfg := &selectCfg{}
	for _, option := range options {
		option(cfg)
	}

	candidates, err := listProviders(ctx, s.storageProviderStore)
	if err != nil {
		return StorageProviderInfo{}, err
	}
	candidates = filterExcludedProviders(candidates, append(slices.Clone(cfg.exclusions), primary))
	candidates = filterZeroReplicationWeightProviders(candidates)
	if len(candidates) == 0 {
		return StorageProviderInfo{}, ErrCandidateUnavailable
	}

	var weights []uint64
	for _, c := range candidates {
		weights = append(weights, uint64(c.Weight))
	}
	selected := candidates[weightedRandomInt(weights)]
	return StorageProviderInfo{
		ID:       selected.Provider,
		Endpoint: selected.Endpoint,
		Proof:    selected.Proof,
	}, nil
}

func listProviders(ctx context.Context, providerStore storageprovider.Store) ([]storageprovider.Record, error) {
	return store.Collect(ctx, func(ctx context.Context, options store.PaginationConfig) (store.Page[storageprovider.Record], error) {
		opts := []storageprovider.ListOption{}
		if options.Cursor != nil {
			opts = append(opts, storageprovider.WithListCursor(*options.Cursor))
		}
		if options.Limit != nil {
			opts = append(opts, storageprovider.WithListLimit(*options.Limit))
		}
		return providerStore.List(ctx, opts...)
	})
}

func filterExcludedProviders(providers []storageprovider.Record, exclusions []ucan.Principal) []storageprovider.Record {
	var filtered []storageprovider.Record
	for _, prov := range providers {
		if prov.Weight <= 0 {
			continue
		}
		excluded := false
		for _, ex := range exclusions {
			if prov.Provider == ex {
				excluded = true
				break
			}
		}
		if !excluded {
			filtered = append(filtered, prov)
		}
	}
	return filtered
}

func filterZeroWeightProviders(providers []storageprovider.Record) []storageprovider.Record {
	var filtered []storageprovider.Record
	for _, prov := range providers {
		if prov.Weight > 0 {
			filtered = append(filtered, prov)
		}
	}
	return filtered
}

func filterZeroReplicationWeightProviders(providers []storageprovider.Record) []storageprovider.Record {
	var filtered []storageprovider.Record
	for _, prov := range providers {
		weight := prov.Weight
		if prov.ReplicationWeight != nil {
			weight = *prov.ReplicationWeight
		}
		if weight > 0 {
			filtered = append(filtered, prov)
		}
	}
	return filtered
}

func weightedRandomInt(weights []uint64) int {
	totalWeight := uint64(0)
	for _, weight := range weights {
		totalWeight += weight
	}
	random := rand.Int63n(int64(totalWeight))
	for i, weight := range weights {
		random -= int64(weight)
		if random <= 0 {
			return i
		}
	}
	panic("did not find a weight - should never reach here")
}

package fx

import (
	"net/url"

	"github.com/fil-forge/go-ucanto/did"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/fil-forge/sprue/internal/config"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/indexerclient"
	"github.com/fil-forge/sprue/pkg/piriclient"
)

// ClientsModule provides external service clients.
var ClientsModule = fx.Module("clients",
	fx.Provide(NewIndexerClient),
	fx.Provide(NewPiriClientProvider),
)

func NewPiriClientProvider(id *identity.Identity, logger *zap.Logger) piriclient.Provider {
	return piriclient.NewProvider(id.Signer, logger)
}

// IndexerClientResult wraps the optional indexer client.
// Using fx.Out with optional tag allows this provider to return nil
// when the indexer is not configured.
type IndexerClientResult struct {
	fx.Out

	Client *indexerclient.Client `optional:"true"`
}

// NewIndexerClient creates an indexer client if configured.
func NewIndexerClient(
	cfg *config.Config,
	id *identity.Identity,
	logger *zap.Logger,
) IndexerClientResult {
	if cfg.Indexer.Endpoint == "" {
		logger.Info("indexer client disabled (no endpoint configured)")
		return IndexerClientResult{}
	}

	indexerURL, err := url.Parse(cfg.Indexer.Endpoint)
	if err != nil {
		logger.Warn("invalid indexer endpoint, client disabled",
			zap.String("endpoint", cfg.Indexer.Endpoint),
			zap.Error(err),
		)
		return IndexerClientResult{}
	}

	var indexerDID did.DID
	if cfg.Indexer.DID != "" {
		// Use explicitly configured DID
		indexerDID, err = did.Parse(cfg.Indexer.DID)
		if err != nil {
			logger.Warn("failed to parse indexer DID",
				zap.String("did", cfg.Indexer.DID),
				zap.Error(err),
			)
		}
	}

	// Fall back to deriving from hostname (without port)
	if indexerDID == (did.DID{}) {
		indexerDID, err = did.Parse("did:web:" + indexerURL.Hostname())
		if err != nil {
			logger.Warn("failed to create indexer DID from hostname",
				zap.String("host", indexerURL.Hostname()),
				zap.Error(err),
			)
			return IndexerClientResult{}
		}
	}

	client, err := indexerclient.New(indexerURL, indexerDID, id.Signer, logger)
	if err != nil {
		logger.Warn("failed to create indexer client",
			zap.Error(err),
		)
		return IndexerClientResult{}
	}

	logger.Info("created indexer client",
		zap.String("endpoint", cfg.Indexer.Endpoint),
		zap.String("did", indexerDID.String()),
	)

	return IndexerClientResult{Client: client}
}

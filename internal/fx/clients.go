package fx

import (
	"net/url"

	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/sprue/internal/config"

	"github.com/fil-forge/sprue/pkg/indexerclient"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/did/web"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// ClientsModule provides external service clients.
var ClientsModule = fx.Module("clients",
	fx.Provide(NewIndexerClient),
	fx.Provide(NewPiriClientProvider),
)

func NewPiriClientProvider(id identity.Identity, logger *zap.Logger) piriclient.Provider {
	return piriclient.NewProvider(id.Issuer, logger)
}

// IndexerClientResult wraps the indexer client. Client is nil when the
// indexer is not configured or its configuration is invalid.
type IndexerClientResult struct {
	fx.Out

	Client *indexerclient.Client
}

// NewIndexerClient creates an indexer client if configured.
func NewIndexerClient(
	cfg *config.Config,
	id identity.Identity,
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

	// Fall back to the did:web of the endpoint: its host, and its port when
	// that is not the scheme's default. The endpoint is trusted configuration
	// and the DID only names the audience of invocations, so an http endpoint
	// is accepted as well as an https one.
	if indexerDID == (did.DID{}) {
		indexerDID, err = web.NewInsecure(*indexerURL)
		if err != nil {
			logger.Warn("failed to create indexer DID from endpoint",
				zap.String("endpoint", cfg.Indexer.Endpoint),
				zap.Error(err),
			)
			return IndexerClientResult{}
		}
	}

	client, err := indexerclient.New(indexerURL, indexerDID, id.Issuer, logger)
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

package fx

import (
	"fmt"

	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/sprue/internal/config"
)

// IdentityModule provides the service identity.
var IdentityModule = fx.Module("identity",
	fx.Provide(NewIdentity),
)

// NewIdentity creates an identity from the configured key file or generates one.
func NewIdentity(cfg *config.Config, logger *zap.Logger) (identity.Identity, error) {
	if cfg.Identity.KeyFile != "" {
		// Use PEM file with optional did:web wrapping
		id, err := identity.NewFromPEMFileWithDID(cfg.Identity.KeyFile, cfg.Identity.ServiceDID)
		if err != nil {
			return identity.Identity{}, fmt.Errorf("identity from key file: %w", err)
		}
		if cfg.Identity.ServiceDID != "" {
			logger.Info("service identity created from PEM file with did:web wrapping",
				zap.String("did", id.DID().String()),
				zap.String("key_file", cfg.Identity.KeyFile),
				zap.String("service_did", cfg.Identity.ServiceDID),
			)
		} else {
			logger.Info("service identity created from PEM file",
				zap.String("did", id.DID().String()),
				zap.String("key_file", cfg.Identity.KeyFile),
			)
		}
		return id, nil
	}

	// Generate or use base64-encoded key
	id, err := identity.New(cfg.Identity.PrivateKey, "")
	if err != nil {
		return identity.Identity{}, fmt.Errorf("creating identity: %w", err)
	}
	logger.Info("service identity created", zap.String("did", id.DID().String()))
	return id, nil
}

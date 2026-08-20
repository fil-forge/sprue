package fx

import (
	"testing"

	"github.com/fil-forge/sprue/internal/config"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestNewIndexerClient(t *testing.T) {
	tests := []struct {
		name   string
		config config.IndexerConfig
		exists bool
	}{
		{
			name: "absent configuration disables client",
		},
		{
			name:   "invalid endpoint disables client",
			config: config.IndexerConfig{Endpoint: "://invalid"},
		},
		{
			name: "valid configuration creates client",
			config: config.IndexerConfig{
				Endpoint: "https://indexer.example.com",
				DID:      "did:web:indexer.example.com",
			},
			exists: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NewIndexerClient(
				&config.Config{Indexer: tt.config},
				testutil.WebService,
				zaptest.NewLogger(t),
			)
			if tt.exists {
				require.NotNil(t, result.Client)
			} else {
				require.Nil(t, result.Client)
			}
		})
	}
}

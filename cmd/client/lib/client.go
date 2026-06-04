package lib

import (
	"fmt"
	"net/url"

	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/sprue/internal/config"
	"github.com/fil-forge/sprue/internal/fx"
	"github.com/fil-forge/sprue/pkg/client"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

func InitClient(cmd *cobra.Command) (*client.Client, *config.Config, *zap.Logger, identity.Identity) {
	var configFile string
	configFlag := cmd.InheritedFlags().Lookup("config")
	if configFlag != nil {
		configFile = configFlag.Value.String()
	}
	cfg, err := config.Load(configFile)
	cobra.CheckErr(err)

	logger, err := fx.NewLogger(cfg)
	cobra.CheckErr(err)
	id, err := fx.NewIdentity(cfg, logger)
	cobra.CheckErr(err)

	endpoint, err := url.Parse(fmt.Sprintf("http://%s:%d", cfg.Server.Host, cfg.Server.Port))
	cobra.CheckErr(err)

	c, err := client.New(id.Issuer.DID(), endpoint, id.Issuer, logger)
	cobra.CheckErr(err)
	return c, cfg, logger, id
}

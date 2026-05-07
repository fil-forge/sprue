package provider

import (
	"net/url"

	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/sprue/cmd/client/lib"
	"github.com/spf13/cobra"
)

var registerCmd = &cobra.Command{
	Use:     "register <provider-url> <proof>",
	Aliases: []string{"add"},
	Short:   "Register a storage provider with the service",
	Args:    cobra.ExactArgs(2),
	RunE:    doRegister,
}

func doRegister(cmd *cobra.Command, args []string) error {
	c, _, _, id := lib.InitClient(cmd)

	endpoint, err := url.Parse(args[0])
	cobra.CheckErr(err)

	proof, err := delegation.Parse(args[1])
	cobra.CheckErr(err)

	_, err = c.AdminProviderRegister(cmd.Context(), id.Signer, endpoint.String(), proof)
	cobra.CheckErr(err)

	cmd.Println("Provider registered successfully")
	return nil
}

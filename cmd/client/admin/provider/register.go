package provider

import (
	"net/url"

	"github.com/fil-forge/sprue/cmd/client/lib"
	"github.com/fil-forge/ucantone/did"
	"github.com/spf13/cobra"
)

var registerCmd = &cobra.Command{
	Use:     "register <provider-did> <provider-url> <proofs>",
	Aliases: []string{"add"},
	Short:   "Register a storage provider with the service",
	Long: "Register a storage provider with the service.\n\n" +
		"<proofs> is a UCAN container granting the upload service `/blob/allocate`,\n" +
		"`/blob/accept` and `/blob/replica/allocate`. It may be passed inline as a\n" +
		"string or as a path to a file containing the encoded container.",
	Args: cobra.ExactArgs(3),
	RunE: doRegister,
}

func doRegister(cmd *cobra.Command, args []string) error {
	c, _, _, _ := lib.InitClient(cmd)

	id, err := did.Parse(args[0])
	cobra.CheckErr(err)

	endpoint, err := url.Parse(args[1])
	cobra.CheckErr(err)

	proofs, err := lib.DecodeProofs(args[2])
	cobra.CheckErr(err)

	_, err = c.AdminProviderRegister(cmd.Context(), id, endpoint.String(), proofs)
	cobra.CheckErr(err)

	cmd.Println("Provider registered successfully")
	return nil
}

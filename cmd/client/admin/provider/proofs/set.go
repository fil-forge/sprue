package proofs

import (
	"github.com/fil-forge/sprue/cmd/client/lib"
	"github.com/fil-forge/ucantone/did"
	"github.com/spf13/cobra"
)

var setCmd = &cobra.Command{
	Use:   "set <provider-did> <proofs>",
	Short: "Replace the proofs a registered storage provider granted the service",
	Long: "Replace the proofs a registered storage provider granted the service.\n\n" +
		"<proofs> must grant every command registration requires. It may be passed\n" +
		"inline as a string or as a path to a file containing the encoded container.",
	Args: cobra.ExactArgs(2),
	RunE: doSet,
}

func doSet(cmd *cobra.Command, args []string) error {
	c, _, _, _ := lib.InitClient(cmd)

	providerID, err := did.Parse(args[0])
	cobra.CheckErr(err)

	proofs, err := lib.DecodeProofs(args[1])
	cobra.CheckErr(err)

	_, err = c.AdminProviderProofsSet(cmd.Context(), providerID, proofs)
	cobra.CheckErr(err)

	cmd.Println("Provider proofs set successfully")
	return nil
}

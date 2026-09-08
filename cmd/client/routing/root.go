package routing

import (
	ucanlib "github.com/fil-forge/libforge/ucan"
	"github.com/fil-forge/sprue/cmd/client/lib"
	"github.com/spf13/cobra"
)

var Cmd = &cobra.Command{
	Use:   "routing",
	Short: "Manage routing policies",
	Long: "Manage routing policies.\n\n" +
		"Invocations are signed with the service identity, so the subject (policy or\n" +
		"space) must have delegated the command to it. Pass the delegation chain with\n" +
		"--proofs, either inline as an encoded UCAN container or as a path to a file\n" +
		"containing one.",
}

func init() {
	Cmd.PersistentFlags().String("proofs", "", "UCAN container holding the proof chain from the subject to the service identity (inline or file path)")
	Cmd.AddCommand(putCmd)
	Cmd.AddCommand(useCmd)
}

// proofStore returns the proof store for the --proofs flag, or nil when the
// flag is unset.
func proofStore(cmd *cobra.Command) ucanlib.ProofStore {
	arg, err := cmd.Flags().GetString("proofs")
	cobra.CheckErr(err)
	if arg == "" {
		return nil
	}
	proofs, err := lib.DecodeProofs(arg)
	cobra.CheckErr(err)
	return ucanlib.NewContainerProofStore(proofs)
}

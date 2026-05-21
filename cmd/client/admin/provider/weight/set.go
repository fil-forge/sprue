package weight

import (
	"strconv"

	"github.com/fil-forge/sprue/cmd/client/lib"
	"github.com/fil-forge/ucantone/did"
	"github.com/spf13/cobra"
)

var setCmd = &cobra.Command{
	Use:   "set <provider-did> <weight> <replication-weight>",
	Short: "Set the weight of a storage provider",
	Args:  cobra.ExactArgs(3),
	RunE:  doSet,
}

func doSet(cmd *cobra.Command, args []string) error {
	c, _, _, _ := lib.InitClient(cmd)

	providerID, err := did.Parse(args[0])
	cobra.CheckErr(err)

	weight, err := strconv.ParseInt(args[1], 10, 0)
	cobra.CheckErr(err)

	replicationWeight, err := strconv.ParseInt(args[2], 10, 0)
	cobra.CheckErr(err)

	_, err = c.AdminProviderWeightSet(cmd.Context(), providerID, int(weight), int(replicationWeight))
	cobra.CheckErr(err)

	cmd.Println("Provider weight set successfully")
	return nil
}

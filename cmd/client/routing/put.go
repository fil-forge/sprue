package routing

import (
	"github.com/fil-forge/sprue/cmd/client/lib"
	"github.com/fil-forge/ucantone/did"
	"github.com/spf13/cobra"
)

var putCmd = &cobra.Command{
	Use:   "put <policy-did> <node-did>...",
	Short: "Replace the candidate storage nodes of a routing policy",
	Long: "Replace the candidate storage nodes of a routing policy.\n\n" +
		"The policy is created on its first put. Every node must be a storage\n" +
		"provider registered with the service.",
	Args: cobra.MinimumNArgs(2),
	RunE: doPut,
}

func doPut(cmd *cobra.Command, args []string) error {
	c, _, _, _ := lib.InitClient(cmd)

	policy, err := did.Parse(args[0])
	cobra.CheckErr(err)

	nodes := make([]did.DID, 0, len(args)-1)
	for _, arg := range args[1:] {
		node, err := did.Parse(arg)
		cobra.CheckErr(err)
		nodes = append(nodes, node)
	}

	_, err = c.RoutingPut(cmd.Context(), policy, nodes, proofStore(cmd))
	cobra.CheckErr(err)

	cmd.Println("Routing policy put successfully")
	return nil
}

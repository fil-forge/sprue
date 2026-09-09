package routing

import (
	"github.com/fil-forge/sprue/cmd/client/lib"
	"github.com/fil-forge/ucantone/did"
	"github.com/spf13/cobra"
)

var useCmd = &cobra.Command{
	Use:   "use <space-did> [policy-did]",
	Short: "Set or clear the routing policy a space uses",
	Long: "Set or clear the routing policy a space uses.\n\n" +
		"With a policy DID, every write to the space is routed to one of the policy's\n" +
		"candidates. Without one, the space returns to default routing.",
	Args: cobra.RangeArgs(1, 2),
	RunE: doUse,
}

func doUse(cmd *cobra.Command, args []string) error {
	c, _, _, _ := lib.InitClient(cmd)

	space, err := did.Parse(args[0])
	cobra.CheckErr(err)

	var policy *did.DID
	if len(args) == 2 {
		p, err := did.Parse(args[1])
		cobra.CheckErr(err)
		policy = &p
	}

	_, err = c.RoutingUse(cmd.Context(), space, policy, proofStore(cmd))
	cobra.CheckErr(err)

	if policy == nil {
		cmd.Println("Routing policy cleared successfully")
	} else {
		cmd.Println("Routing policy set successfully")
	}
	return nil
}

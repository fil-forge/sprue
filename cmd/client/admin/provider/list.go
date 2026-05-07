package provider

import (
	"fmt"

	"github.com/fil-forge/sprue/cmd/client/lib"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List registered storage providers",
	Args:    cobra.NoArgs,
	RunE:    doList,
}

func doList(cmd *cobra.Command, args []string) error {
	c, _, _, id := lib.InitClient(cmd)

	res, err := c.AdminProviderList(cmd.Context(), id.Signer)
	cobra.CheckErr(err)

	if len(res.Providers) == 0 {
		cmd.Println("No providers registered")
		return nil
	}

	table := lib.NewTable(cmd.OutOrStdout())
	table.SetHeader([]string{"ID", "Weight", "Replication Weight", "URL"})
	for _, p := range res.Providers {
		table.Append([]string{p.ID.String(), fmt.Sprintf("%d", p.Weight), fmt.Sprintf("%d", p.ReplicationWeight), p.Endpoint})
	}
	table.Render()

	return nil
}

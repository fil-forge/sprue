package admin

import (
	"github.com/fil-forge/sprue/cmd/client/admin/provider"
	"github.com/spf13/cobra"
)

var Cmd = &cobra.Command{
	Use:   "admin",
	Short: "Administrate a running sprue via UCAN invocations",
}

func init() {
	Cmd.AddCommand(provider.Cmd)
}

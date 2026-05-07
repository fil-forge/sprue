package client

import (
	"github.com/fil-forge/sprue/cmd/client/admin"
	"github.com/spf13/cobra"
)

var Cmd = &cobra.Command{
	Use:   "client",
	Short: "Interact with a running sprue via UCAN invocations",
}

func init() {
	Cmd.AddCommand(admin.Cmd)
}

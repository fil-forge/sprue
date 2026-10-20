package proofs

import (
	"github.com/spf13/cobra"
)

var Cmd = &cobra.Command{
	Use:   "proofs",
	Short: "Manage the proofs storage providers grant the service",
}

func init() {
	Cmd.AddCommand(setCmd)
}

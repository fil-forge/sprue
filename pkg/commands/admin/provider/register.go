package provider

import (
	"github.com/fil-forge/libforge/commands"
	cdm "github.com/fil-forge/libforge/commands"
	pdm "github.com/fil-forge/sprue/pkg/commands/admin/provider/datamodel"
)

type (
	RegisterArguments = pdm.RegisterArgumentsModel
	RegisterOK        = cdm.Unit
)

var Register = commands.MustParse[*RegisterArguments]("/admin/provider/register")

package provider

import (
	"github.com/fil-forge/libforge/commands"
	pdm "github.com/fil-forge/sprue/pkg/commands/admin/provider/datamodel"
)

type (
	ListArguments = pdm.ListArgumentsModel
	ListOK        = pdm.ListOKModel
	Provider      = pdm.ProviderModel
)

var List = commands.MustParse[*ListArguments]("/admin/provider/list")

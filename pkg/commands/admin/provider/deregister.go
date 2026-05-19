package provider

import (
	"github.com/fil-forge/libforge/commands"
	pdm "github.com/fil-forge/sprue/pkg/commands/admin/provider/datamodel"
)

type (
	DeregisterArguments = pdm.DeregisterArgumentsModel
	DeregisterOK        = commands.Unit
)

var Deregister = commands.MustParse[*DeregisterArguments]("/admin/provider/deregister")

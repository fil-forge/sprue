package weight

import (
	"github.com/fil-forge/libforge/commands"
	wdm "github.com/fil-forge/sprue/pkg/commands/admin/provider/weight/datamodel"
)

type (
	SetArguments = wdm.SetArgumentsModel
	SetOK        = commands.Unit
)

var Set = commands.MustParse[*SetArguments]("/provider/weight/set")

package provider

import (
	pdm "github.com/fil-forge/sprue/pkg/capabilities/admin/provider/datamodel"
	"github.com/fil-forge/ucantone/validator/bindcap"
)

const ListCommand = "/admin/provider/list"

type (
	ListArguments = pdm.ListArgumentsModel
	ListOK        = pdm.ListOKModel
	Provider      = pdm.ProviderModel
)

var List, _ = bindcap.New[*ListArguments](ListCommand)

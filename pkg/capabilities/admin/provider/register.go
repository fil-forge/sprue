package provider

import (
	cdm "github.com/fil-forge/libforge/capabilities"
	pdm "github.com/fil-forge/sprue/pkg/capabilities/admin/provider/datamodel"
	"github.com/fil-forge/ucantone/validator/bindcap"
)

const RegisterCommand = "/admin/provider/register"

type (
	RegisterArguments = pdm.RegisterArgumentsModel
	RegisterOK        = cdm.Unit
)

var Register, _ = bindcap.New[*RegisterArguments](RegisterCommand)

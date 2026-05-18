package provider

import (
	cdm "github.com/fil-forge/libforge/capabilities"
	pdm "github.com/fil-forge/sprue/pkg/capabilities/admin/provider/datamodel"
	"github.com/fil-forge/ucantone/validator/bindcap"
)

const DeregisterCommand = "/admin/provider/deregister"

type (
	DeregisterArguments = pdm.DeregisterArgumentsModel
	DeregisterOK        = cdm.Unit
)

var Deregister, _ = bindcap.New[*DeregisterArguments](DeregisterCommand)

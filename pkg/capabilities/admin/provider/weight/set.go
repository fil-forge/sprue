package weight

import (
	cdm "github.com/fil-forge/libforge/capabilities/datamodel"
	wdm "github.com/fil-forge/sprue/pkg/capabilities/admin/provider/weight/datamodel"
	"github.com/fil-forge/ucantone/ucan/delegation/policy"
	"github.com/fil-forge/ucantone/validator/bindcap"
	"github.com/fil-forge/ucantone/validator/capability"
)

const SetCommand = "/provider/weight/set"

type (
	SetArguments = wdm.SetArgumentsModel
	SetOK        = cdm.UnitModel
)

var Set, _ = bindcap.New[*SetArguments](
	SetCommand,
	capability.WithPolicyBuilder(
		policy.GreaterThanOrEqual(".weight", 0),
		policy.GreaterThanOrEqual(".replicationWeight", 0),
	),
)

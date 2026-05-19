//go:build !codegen

package weight

import "github.com/fil-forge/libforge/commands"

type SetOK = commands.Unit

var Set = commands.MustParse[*SetArguments]("/provider/weight/set")

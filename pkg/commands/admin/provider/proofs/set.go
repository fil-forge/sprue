//go:build !codegen

package proofs

import (
	"github.com/fil-forge/libforge/commands"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/ucan/command"
)

type SetOK = commands.Unit

var Set = binding.Bind[*SetArguments, *SetOK](command.MustParse("/admin/provider/proofs/set"))

//go:build !codegen

package provider

import (
	"github.com/fil-forge/libforge/commands"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/ucan/command"
)

type RegisterOK = commands.Unit

var Register = binding.Bind[*RegisterArguments, *RegisterOK](command.MustParse("/admin/provider/register"))

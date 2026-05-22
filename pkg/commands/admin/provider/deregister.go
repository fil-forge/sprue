//go:build !codegen

package provider

import (
	"github.com/fil-forge/libforge/commands"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/ucan/command"
)

type DeregisterOK = commands.Unit

var Deregister = binding.Bind[*DeregisterArguments, *DeregisterOK](command.MustParse("/admin/provider/deregister"))

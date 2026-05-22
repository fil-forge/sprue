//go:build !codegen

package provider

import (
	"github.com/fil-forge/libforge/commands"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/ucan/command"
)

type ListArguments = commands.Unit

var List = binding.Bind[*ListArguments, *ListOK](command.MustParse("/admin/provider/list"))

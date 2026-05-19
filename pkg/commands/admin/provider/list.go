//go:build !codegen

package provider

import "github.com/fil-forge/libforge/commands"

type ListArguments = commands.Unit

var List = commands.MustParse[*ListArguments]("/admin/provider/list")

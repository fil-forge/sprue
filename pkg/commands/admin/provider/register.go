//go:build !codegen

package provider

import "github.com/fil-forge/libforge/commands"

type RegisterOK = commands.Unit

var Register = commands.MustParse[*RegisterArguments]("/admin/provider/register")

//go:build !codegen

package provider

import "github.com/fil-forge/libforge/commands"

type DeregisterOK = commands.Unit

var Deregister = commands.MustParse[*DeregisterArguments]("/admin/provider/deregister")

// Package template provides embedded project and service templates for the Sling CLI.
package template

import "embed"

// FS embeds the project and service scaffolding templates for the Sling CLI.
//
//go:embed all:project all:service
var FS embed.FS

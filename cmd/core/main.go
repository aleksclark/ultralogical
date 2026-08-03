// core is the ultracore command-line client.
//
// It is a consumer of the public API and nothing more: every command is a call
// through the generated Connect client, with no database access and no
// server-side shortcut. If the public API cannot express something, neither can
// this CLI. Configuration:
//
//	CORE_URL    cored base URL (default http://localhost:8080)
//	CORE_TOKEN  bearer token (required)
//	CORE_ORG    default org id for org-scoped commands
//
// Every command accepts --json for machine-readable output and exits nonzero on
// a typed failure, so scripts can branch on the failure rather than the text.
package main

import (
	"fmt"
	"os"

	"github.com/aleksclark/ultracore/cmd/core/cli"
)

func main() {
	code, err := cli.Run(os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ultra:", err)
	}
	os.Exit(code)
}

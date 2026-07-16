// Command harness is the published CLI for developing Go/Python/TypeScript
// software with AI coding agents. It scaffolds and manages clean projects that
// never vendor the tool itself. See the internal/cli package for dispatch.
package main

import (
	"os"

	"github.com/olesho/harness/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

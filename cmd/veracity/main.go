// Command veracity is the published CLI for developing Go/Python/TypeScript
// software with AI coding agents. It scaffolds and manages clean projects that
// never vendor the tool itself. See the internal/cli package for dispatch.
package main

import (
	"os"

	"github.com/olesho/veracity/internal/cli"
)

func main() {
	// A reader that closes early — a piped `git push`, `veracity ci | head`, a
	// captured hook run — must not kill us with SIGPIPE (exit 141) mid-verify:
	// with the signal ignored, writes to fd 1/2 return EPIPE (discarded by our
	// fmt.Fprint calls) and the command still reports its true exit code.
	ignoreSIGPIPE()
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

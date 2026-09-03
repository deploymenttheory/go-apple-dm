// Command mdmctl administers a go-apple-mdm reference server.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/deploymenttheory/go-apple-mdm/internal/mdmctl"
)

func main() {
	err := mdmctl.Run(context.Background(), os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mdmctl:", err)
	}
	os.Exit(mdmctl.ExitCode(err))
}

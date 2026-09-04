// Command dmctl administers a go-apple-dm reference server.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/deploymenttheory/go-apple-dm/v3/internal/dmctl"
)

func main() {
	err := dmctl.Run(context.Background(), os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dmctl:", err)
	}
	os.Exit(dmctl.ExitCode(err))
}

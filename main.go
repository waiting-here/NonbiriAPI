// Command nonbiriapi serves the user and administrator sites.
package main

import (
	"os"

	"github.com/waiting-here/NonbiriAPI/internal/app"
)

func main() {
	os.Exit(app.Run(os.Args[1:], os.Stdout, os.Stderr))
}

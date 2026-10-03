// Package app composes the application and owns its process lifecycle.
package app

import (
	"fmt"
	"io"
)

// Run executes a maintenance command or serves the configured application.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		if err := runMaintenance(args, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	return run()
}

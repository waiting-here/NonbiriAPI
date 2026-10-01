// Command nonbiriapi opens a validated Generation 2 database and serves the
// two host-isolated station shells. Domain APIs are registered by their
// Generation 2 owners only after their persistence and lifecycle contracts
// are complete.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 {
		if err := runMaintenance(os.Args[1:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	os.Exit(run())
}

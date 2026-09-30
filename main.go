// Command nonbiriapi opens a validated Generation 2 database and serves the
// two host-isolated station shells. Domain APIs are registered by their
// Generation 2 owners only after their persistence and lifecycle contracts
// are complete.
package main

import "os"

func main() {
	os.Exit(run())
}

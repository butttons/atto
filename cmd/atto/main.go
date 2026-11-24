// Command atto edits files through snapshot-guarded, line-addressed plans.
//
// All behaviour lives in internal/atto so it can be tested; this file only
// hands the process exit code back to the OS.
package main

import (
	"os"

	"github.com/butttons/atto/internal/atto"
)

func main() {
	os.Exit(atto.Run(os.Args[1:]))
}

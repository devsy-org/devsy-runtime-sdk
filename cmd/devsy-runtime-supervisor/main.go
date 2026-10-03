// devsy-runtime-supervisor owns one plugin process tree for one host lease.
package main

import (
	"os"

	"github.com/devsy-org/devsy-runtime-sdk/supervisor"
)

func main() { supervisor.Main(os.Args[1:]) }

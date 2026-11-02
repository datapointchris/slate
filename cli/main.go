package main

import (
	"os"

	"github.com/datapointchris/slate/cli/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}

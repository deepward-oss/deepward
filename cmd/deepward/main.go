// Command deepward is a universal checks orchestrator for CI.
package main

import (
	"os"

	"github.com/deepward-oss/deepward/pkg/app"
	_ "github.com/deepward-oss/deepward/plugins/all" // built-in plugins
)

func main() { os.Exit(app.Main(os.Args[1:])) }

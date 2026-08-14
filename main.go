package main

import (
	_ "github.com/auto-deployer/auto-deployer/plugins"
	"github.com/auto-deployer/auto-deployer/cmd"
)

func main() {
	cmd.Execute()
}

package main

import (
	"fmt"
	"os"

	"github.com/mitchellh/cli"

	"github.com/nerdalize/git-bits/command"
)

var (
	name    = "git-bits"
	version = "0.0.0"
)

func main() {
	c := cli.NewCLI(name, version)
	c.Args = os.Args[1:]
	c.Commands = map[string]cli.CommandFactory{
		"scan":     command.NewScan,
		"split":    command.NewSplit,
		"install":  command.NewInstall,
		"fetch":    command.NewFetch,
		"pull":     command.NewPull,
		"push":     command.NewPush,
		"combine":  command.NewCombine,
		"checkout": command.NewCheckout,
	}

	status, err := c.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %s", name, err)
	}

	os.Exit(status)
}

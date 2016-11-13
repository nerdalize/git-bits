package main

import (
	"fmt"
	"os"

	"github.com/mitchellh/cli"

	"github.com/nerdalize/git-bits/command/git"
)

var (
	name    = "nerdalize"
	version = "0.0.0"
)

func main() {
	c := cli.NewCLI(name, version)
	c.Args = os.Args[1:]
	c.Commands = map[string]cli.CommandFactory{
		"git prepush": gitcommand.NewPrePush,
		"git clean":   gitcommand.NewClean,
		"git smudge":  gitcommand.NewSmudge,
	}

	status, err := c.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %s", name, err)
	}

	os.Exit(status)
}

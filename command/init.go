package command

import (
	"bytes"
	"fmt"
	"os"

	"github.com/jessevdk/go-flags"
	"github.com/mitchellh/cli"
	"github.com/nerdalize/git-bits/bits"
)

var InitOpts struct {
	//
	Verbose []bool `short:"v" long:"verbose" description:"Show verbose debug information"`

	// Example of automatic marshalling to desired type (uint)
	Offset uint `long:"offset" description:"Offset"`

	// Example of a callback, called each time the option is found.
	Call func(string) `short:"c" description:"Call phone number"`

	// Example of a value name
	File string `short:"f" long:"file" description:"A file" value-name:"FILE"`

	// Example of a pointer
	Ptr *int `short:"p" description:"A pointer to an integer"`

	// Example of a slice of strings
	StringSlice []string `short:"s" description:"A slice of strings"`

	// Example of a slice of pointers
	PtrSlice []*string `long:"ptrslice" description:"A slice of pointers to string"`

	// Example of a map
	IntMap map[string]int `long:"intmap" description:"A map from string to int"`
}

type Init struct {
	ui cli.Ui
}

func NewInit() (cmd cli.Command, err error) {
	return &Init{
		ui: &cli.BasicUi{
			Reader:      os.Stdin,
			Writer:      os.Stderr,
			ErrorWriter: os.Stderr,
		},
	}, nil
}

// Help returns long-form help text that includes the command-line
// usage, a brief few sentences explaining the function of the command,
// and the complete list of flags the command accepts.
func (cmd *Init) Help() string {
	parser := flags.NewNamedParser(cmd.Usage(), flags.PassDoubleDash)
	_, err := parser.AddGroup("default", "", &InitOpts)
	if err != nil {
		panic(err)
	}

	buf := bytes.NewBuffer(nil)
	parser.WriteHelp(buf)

	return fmt.Sprintf(`
  %s

%s
`, cmd.Synopsis(), buf.String())
}

// Synopsis returns a one-line, short synopsis of the command.
// This should be less than 50 characters ideally.
func (cmd *Init) Synopsis() string {
	return "configures filters, create pre-push hook and pull chunks"
}

// Usage returns a usage description
func (cmd *Init) Usage() string {
	return "git bits init"
}

// Run runs the actual command with the given CLI instance and
// command-line arguments. It returns the exit status when it is
// finished.
func (cmd *Init) Run(args []string) int {
	args, err := flags.ParseArgs(&InitOpts, args)
	if err != nil {
		cmd.ui.Error(fmt.Sprintf("failed to parse flags: %v", err))
		return 1
	}

	wd, err := os.Getwd()
	if err != nil {
		cmd.ui.Error(fmt.Sprintf("failed to get working directory: %v", err))
		return 2
	}

	repo, err := bits.NewRepository(wd)
	if err != nil {
		cmd.ui.Error(fmt.Sprintf("failed to setup repository: %v", err))
		return 3
	}

	err = repo.Init(os.Stdout)
	if err != nil {
		cmd.ui.Error(fmt.Sprintf("failed to fetch: %v", err))
		return 4
	}

	return 0
}

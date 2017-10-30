package command

import (
	"fmt"
	"os"

	"github.com/mitchellh/cli"
	"github.com/jungleai/git-bits/bits"
)

type Combine struct {
	ui cli.Ui
}

func NewCombine() (cmd cli.Command, err error) {
	return &Combine{
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
func (cmd *Combine) Help() string {
	return fmt.Sprintf(`
  %s
`, cmd.Synopsis())
}

// Synopsis returns a one-line, short synopsis of the command.
// This should be less than 50 characters ideally.
func (cmd *Combine) Synopsis() string {
	return "combine chunks back into the original file"
}

// Run runs the actual command with the given CLI instance and
// command-line arguments. It returns the exit status when it is
// finished.
func (cmd *Combine) Run(args []string) int {
	wd, err := os.Getwd()
	if err != nil {
		cmd.ui.Error(fmt.Sprintf("Failed to get working directory: %v", err))
		return 1
	}

	repo, err := bits.NewRepository(wd, os.Stderr)
	if err != nil {
		cmd.ui.Error(fmt.Sprintf("failed to setup repository: %v", err))
		return 2
	}

	err = repo.Combine(os.Stdin, os.Stdout)
	if err != nil {
		cmd.ui.Error(fmt.Sprintf("failed to combine: %v", err))
		return 3
	}

	return 0
}

package command

import (
	"fmt"
	"os"

	"github.com/mitchellh/cli"
	"github.com/jungleai/git-bits/bits"
)

type Scan struct {
	ui cli.Ui
}

func NewScan() (cmd cli.Command, err error) {
	return &Scan{
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
func (cmd *Scan) Help() string {
	return fmt.Sprintf(`
  %s
`, cmd.Synopsis())
}

// Synopsis returns a one-line, short synopsis of the command.
// This should be less than 50 characters ideally.
func (cmd *Scan) Synopsis() string {
	return "queries the git database for all chunk keys in blobs"
}

// Run runs the actual command with the given CLI instance and
// command-line arguments. It returns the exit status when it is
// finished.
func (cmd *Scan) Run(args []string) int {
	wd, err := os.Getwd()
	if err != nil {
		cmd.ui.Error(fmt.Sprintf("failed to get working directory: %v", err))
		return 1
	}

	repo, err := bits.NewRepository(wd, os.Stderr)
	if err != nil {
		cmd.ui.Error(fmt.Sprintf("failed to setup repository: %v", err))
		return 2
	}

	// if len(args) < 1 {
	// 	cmd.ui.Error(fmt.Sprintf("expected at least 1 arguments, got: %v", args))
	// 	return 128
	// }

	err = repo.ScanEach(os.Stdin, os.Stdout)
	if err != nil {
		cmd.ui.Error(fmt.Sprintf("failed to scan: %v", err))
		return 3
	}

	// right := args[0]
	// left := ""
	// if len(args) > 1 {
	// 	left = args[1]
	// }
	//
	// err = repo.Scan(left, right, os.Stdout)
	// if err != nil {
	// 	cmd.ui.Error(fmt.Sprintf("failed to scan: %v", err))
	// 	return 3
	// }

	return 0
}

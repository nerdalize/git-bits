package command

import (
	"bytes"
	"fmt"
	"os"

	"github.com/jessevdk/go-flags"
	"github.com/mitchellh/cli"
	"github.com/jungleai/git-bits/bits"
)

var InstallOpts struct {
	// Name of the s3 bucket that will be configured for the remote
	Bucket string `short:"b" long:"bucket" description:"name of the s3 bucket used as a chunk remote"`

	// Chunk remote will be configured for configuration under this remote
	Remote string `short:"r" long:"remote" default:"origin" required:"true" description:"git remote that will be configured for chunk storage (default=origin)"`
}

type Install struct {
	ui cli.Ui
}

func NewInstall() (cmd cli.Command, err error) {
	return &Install{
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
func (cmd *Install) Help() string {
	parser := flags.NewNamedParser(cmd.Usage(), flags.PassDoubleDash)
	_, err := parser.AddGroup("default", "", &InstallOpts)
	if err != nil {
		panic(err)
	}

	buf := bytes.NewBuffer(nil)
	parser.WriteHelp(buf)

	return fmt.Sprintf(`
  %s

%s`, cmd.Synopsis(), buf.String())
}

// Synopsis returns a one-line, short synopsis of the command.
// This should be less than 50 characters ideally.
func (cmd *Install) Synopsis() string {
	return "configures filters, create pre-push hook and pull chunks"
}

// Usage returns a usage description
func (cmd *Install) Usage() string {
	return "git bits init"
}

// Run runs the actual command with the given CLI instance and
// command-line arguments. It returns the exit status when it is
// finished.
func (cmd *Install) Run(args []string) int {
	args, err := flags.ParseArgs(&InstallOpts, args)
	if err != nil {
		cmd.ui.Error(fmt.Sprintf("failed to parse flags: %v", err))
		return 1
	}

	wd, err := os.Getwd()
	if err != nil {
		cmd.ui.Error(fmt.Sprintf("failed to get working directory: %v", err))
		return 2
	}

	repo, err := bits.NewRepository(wd, os.Stderr)
	if err != nil {
		cmd.ui.Error(fmt.Sprintf("failed to setup repository: %v", err))
		return 3
	}

	conf := bits.DefaultConf()
	conf.AWSS3BucketName, err = cmd.ui.Ask("In which AWS S3 bucket would you like to store chunks? \n")
	if err != nil {
		cmd.ui.Error(fmt.Sprintf("failed to get input: %v", err))
		return 128
	}

	conf.AWSAccessKeyID, err = cmd.ui.Ask("What is your AWS Access Key ID with list, read and write access to the above bucket? \n")
	if err != nil {
		cmd.ui.Error(fmt.Sprintf("failed to get input: %v", err))
		return 128
	}

	conf.AWSRegion, err = cmd.ui.Ask("What is the AWS region where the bucket is located?\n")
	if err != nil {
		cmd.ui.Error(fmt.Sprintf("failed to get input: %v", err))
		return 128
	}

	conf.AWSSecretAccessKey, err = cmd.ui.AskSecret("What is your AWS Secret Key that autorizes the above access key? (input will be hidden)\n")
	if err != nil {
		cmd.ui.Error(fmt.Sprintf("failed to get input: %v", err))
		return 128
	}

	err = repo.Install(os.Stdout, conf)
	if err != nil {
		cmd.ui.Error(fmt.Sprintf("failed to fetch: %v", err))
		return 4
	}

	return 0
}

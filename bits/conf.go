package bits

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
)

//Conf for the bits repository we're using
type Conf struct {

	//holds the aws s3 bucket name
	AWSS3BucketName string `json:"aws_s3_bucket_name"`

	//holds the aws s3 bucket region
	AWSRegion string `json:"aws_s3_bucket_region"`

	//The aws key that has access to the above bucket
	AWSAccessKeyID string `json:"aws_access_key_id"`

	//the aws secret that authorizes access to the s3 bucket
	AWSSecretAccessKey string `json:"aws_secret_access_key"`

	//holds the chunking polynomial
	DeduplicationScope uint64 `json:"deduplication_scope"`
}

//DefaultConf will setup a default configuration
func DefaultConf() *Conf {
	return &Conf{
		DeduplicationScope: 0x3DA3358B4DC173,
	}
}

//LoadGitValues will overwrite values based on configuration
//set through git
func (conf *Conf) OverwriteFromGit(repo *Repository) (err error) {
	buf := bytes.NewBuffer(nil)
	err = repo.Git(context.Background(), nil, buf, "config", "--get-regexp", "^bits")
	if err != nil {
		return nil //no bits conf, nothing to do
	}

	s := bufio.NewScanner(buf)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 2 {
			return fmt.Errorf("unexpected configuration returned from git: %v", s.Text())
		}

		switch fields[0] {
		case "bits.deduplication-scope":
			scope, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return fmt.Errorf("unexpected format for configured dedup scope '%v', expected a base10 number", fields[1])
			}

			conf.DeduplicationScope = scope
		case "bits.aws-s3-bucket-name":
			conf.AWSS3BucketName = fields[1]
		case "bits.aws-access-key-id":
			conf.AWSAccessKeyID = fields[1]
		case "bits.aws-secret-access-key":
			conf.AWSSecretAccessKey = fields[1]
		case "bits.aws-s3-bucket-region":
			conf.AWSRegion = fields[1]
		}
	}

	return nil
}

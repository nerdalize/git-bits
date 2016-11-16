package bits

import (
	"fmt"
	"io"
	"os"

	"github.com/rlmcpherson/s3gof3r"
)

type S3Remote struct {
	gitRemote string
	bucket    *s3gof3r.Bucket
	repo      *Repository
	idx       *Index
}

func NewS3Remote(repo *Repository, name string) (s3 *S3Remote, err error) {
	s3 = &S3Remote{
		repo:      repo,
		gitRemote: name,
	}

	//@TODO make git remote we're using configurable
	s3.idx, err = NewIndex(repo, "", s3.gitRemote)
	if err != nil {
		return nil, fmt.Errorf("failed to setup index: %v", err)
	}

	//@TODO make this configurable
	creds, err := s3gof3r.EnvKeys()
	if err != nil {
		return nil, fmt.Errorf("Failed to get s3 credentials from environment: %v", err)
	}

	testBucket := os.Getenv("TEST_BUCKET")
	if testBucket == "" {
		return nil, fmt.Errorf("Failed to get bucket from TEST_BUCKET environment")
	}

	//@TODO make bucket name configurable
	s3.bucket = s3gof3r.New("", creds).Bucket(testBucket)
	return s3, nil
}

func (s3 *S3Remote) Index() *Index {
	return s3.idx
}

func (s3 *S3Remote) Name() string {
	return s3.gitRemote
}

//ChunkReader returns a file handle that the chunk with the given
//key can be read from, the user is expected to close it when finished
func (s *S3Remote) ChunkReader(k K) (rc io.ReadCloser, err error) {
	rc, _, err = s.bucket.GetReader(fmt.Sprintf("%x", k), nil)
	return rc, err
}

//ChunkWriter returns a file handle to which a chunk with give key
//can be written to, the user is expected to close it when finished.
func (s *S3Remote) ChunkWriter(k K) (wc io.WriteCloser, err error) {
	return s.bucket.PutWriter(fmt.Sprintf("%x", k), nil, nil)
}

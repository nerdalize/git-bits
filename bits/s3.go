package bits

import (
	"fmt"
	"io"

	"github.com/rlmcpherson/s3gof3r"
)

type S3Remote struct {
	gitRemote string
	bucket    *s3gof3r.Bucket
	repo      *Repository
	idx       *Index
}

func NewS3Remote(repo *Repository, remote, bucket, accessKey, secretKey string) (s3 *S3Remote, err error) {
	s3 = &S3Remote{
		repo:      repo,
		gitRemote: remote,
	}

	s3.idx, err = NewIndex(repo, "", s3.gitRemote)
	if err != nil {
		return nil, fmt.Errorf("failed to setup index: %v", err)
	}

	s3.bucket = s3gof3r.New("", s3gof3r.Keys{
		AccessKey: accessKey,
		SecretKey: secretKey,
	}).Bucket(bucket)

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

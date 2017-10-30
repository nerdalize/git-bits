package bits

import (
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/rlmcpherson/s3gof3r"
)

type S3Remote struct {
	gitRemote string
	bucket    *s3gof3r.Bucket
	repo      *Repository
}

func NewS3Remote(repo *Repository, remote, bucket, accessKey, secretKey string, domain string) (s3 *S3Remote, err error) {
	s3 = &S3Remote{
		repo:      repo,
		gitRemote: remote,
	}

	s3.bucket = s3gof3r.New(domain, s3gof3r.Keys{
		AccessKey: accessKey,
		SecretKey: secretKey,
	}).Bucket(bucket)

	return s3, nil
}

func (s3 *S3Remote) Name() string {
	return s3.gitRemote
}

//ListChunks will write all chunks in the bucket to writer w
func (s *S3Remote) ListChunks(w io.Writer) (err error) {

	// <?xml version="1.0" encoding="UTF-8"?>
	// <ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
	// 	<Name>nlz-ad3c28975b40bb38-test-bucket</Name>
	// 	<Prefix></Prefix>
	// 	<KeyCount>578</KeyCount>
	// 	<MaxKeys>1000</MaxKeys>
	// 	<IsTruncated>false</IsTruncated>
	// 	<Contents>
	// 		<Key>.md5/0095a2145dbf524ddf22bf0d0bc6a149066d579e96812da393e87fc3696516fc.md5</Key>
	// 		<LastModified>2016-11-19T09:17:17.000Z</LastModified>
	// 		<ETag>&quot;6f1aef3bef9e4a572e18249ed4014a7d&quot;</ETag>
	// 		<Size>32</Size>
	// 		<StorageClass>STANDARD</StorageClass>
	// 	</Contents>
	//  <Contents>
	//    ...
	v := struct {
		XMLName               xml.Name `xml:"ListBucketResult"`
		Name                  string   `xml:"Name"`
		IsTruncated           bool     `xml:"IsTruncated"`
		NextContinuationToken string   `xml:"NextContinuationToken"`
		Contents              []struct {
			Key string `xml:"Key"`
		} `xml:"Contents"`
	}{}

	next := ""
	for {
		q := url.Values{}
		q.Set("list-type", "2")
		q.Set("max-keys", "500")
		if next != "" {
			q.Set("continuation-token", next)
		}

		loc := fmt.Sprintf("%s://%s.%s/?%s", s.bucket.Scheme, s.bucket.Name, s.bucket.Domain, q.Encode())
		req, err := http.NewRequest("GET", loc, nil)
		if err != nil {
			return fmt.Errorf("failed to create listing request: %v", err)
		}

		s.bucket.Sign(req)
		resp, err := s.bucket.Client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to request bucket list: %v", err)
		}

		defer resp.Body.Close()
		dec := xml.NewDecoder(resp.Body)
		err = dec.Decode(&v)
		if err != nil {
			return fmt.Errorf("failed to decode s3 xml: %v")
		}

		for _, obj := range v.Contents {
			if len(obj.Key) != hex.EncodedLen(KeySize) {
				continue
			}

			fmt.Fprintf(w, "%s\n", obj.Key)
		}

		v.Contents = nil
		if !v.IsTruncated {
			break
		}

		next = v.NextContinuationToken
	}

	return nil
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

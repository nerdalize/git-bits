package bits_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	mrand "math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdalize/git-bits/bits"
)

func GitInitRemote(t *testing.T) (dir string) {
	dir, err := ioutil.TempDir("", "test_remote_")
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("git", "init", "--bare")
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		t.Fatal(err)
	}

	return dir
}

func GitCloneWorkspace(remote string, t *testing.T) (dir string, repo *bits.Repository) {
	dir, err := ioutil.TempDir("", "test_remote_")
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("git", "clone", remote, dir)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		t.Fatal(err)
	}

	repo, err = bits.NewRepository(dir, nil)
	if err != nil {
		t.Fatal(err)
	}

	return dir, repo
}

func GitConfigure(t *testing.T, ctx context.Context, repo *bits.Repository, conf map[string]string) {
	for k, val := range conf {
		err := repo.Git(ctx, nil, nil, "config", "--local", k, val)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func WriteGitAttrFile(t *testing.T, dir string, attr map[string]string) {
	f, err := os.Create(filepath.Join(dir, ".gitattributes"))
	if err != nil {
		t.Fatal(err)
	}

	defer f.Close()
	for pattern, attr := range attr {
		fmt.Fprintf(f, "%s\t%s\n", pattern, attr)
	}
}

func BuildBinaryInPath(t *testing.T, ctx context.Context) {
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		t.Fatalf("GOPATH not set for building git-bits for integration test, env: %+v", os.Environ())
	}

	cmd := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(gopath, "bin", "git-bits"))
	cmd.Dir = filepath.Join(gopath, "src", "github.com", "nerdalize", "git-bits")
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		t.Fatalf("failed to build git-bits, make sure this project is in $GOPATH/src/github.com/nerdalize/git-bits: %v", err)
	}

}

func WriteRandomFile(t *testing.T, path string, size int64) (f *os.File) {
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	randr := io.LimitReader(rand.Reader, size)
	_, err = io.Copy(f, randr)
	if err != nil {
		t.Fatal(err)
	}

	return f
}

func TestNewRepository(t *testing.T) {
	_, err := bits.NewRepository("/tmp/my-bogus-repo", nil)
	if err == nil {
		t.Errorf("creating repo in non-existing directory should fail")
	} else {
		if !strings.Contains(err.Error(), "git repository") {
			t.Errorf("creating repo should fail with non existing dir error, got: %v", err)
		}
	}

	tdir, _ := ioutil.TempDir("", "test_wdir_")
	_, err = bits.NewRepository(tdir, nil)
	if err == nil {
		t.Errorf("creating repo in non-git directory should fail")
	} else {
		if !strings.Contains(err.Error(), "git repository") {
			t.Errorf("creating repo should fail with exit code, got: %v", err)
		}
	}
}

//test basic file splitting and combining
func TestSplitCombineScan(t *testing.T) {
	ctx := context.Background()
	ctx, _ = context.WithTimeout(ctx, time.Second*10)

	BuildBinaryInPath(t, ctx) //@TODO this is terrible for unit testing

	remote1 := GitInitRemote(t)
	wd1, repo1 := GitCloneWorkspace(remote1, t)
	WriteGitAttrFile(t, wd1, map[string]string{
		"*.bin": "filter=bits",
	})

	err := repo1.Init(os.Stderr, bits.DefaultConf())
	if err != nil {
		t.Error(err)
	}

	fpath := filepath.Join(wd1, "file1.bin")
	f1 := WriteRandomFile(t, fpath, 5*1024*1024)
	f1.Close()

	err = repo1.Git(ctx, nil, nil, "add", "-A")
	if err != nil {
		t.Error(err)
	}

	err = repo1.Git(ctx, nil, nil, "commit", "-m", "c0")
	if err != nil {
		t.Error(err)
	}

	c0buf := bytes.NewBuffer(nil)
	err = repo1.Git(ctx, nil, c0buf, "rev-parse", "HEAD")
	if err != nil {
		t.Error(err)
	}

	c0 := strings.TrimSpace(c0buf.String())
	originalContent := bytes.NewBuffer(nil)

	f2, err := os.OpenFile(fpath, os.O_RDWR, 0666)
	if err != nil {
		t.Error(err)
	}

	_, err = io.Copy(originalContent, f2)
	if err != nil {
		t.Error(err)
	}

	_, err = f2.WriteAt([]byte{0x00}, 5)
	if err != nil {
		t.Error(err)
	}

	f2.Close()

	err = repo1.Git(ctx, nil, nil, "add", "-A")
	if err != nil {
		t.Error(err)
	}

	err = repo1.Git(ctx, nil, nil, "commit", "-m", "c1")
	if err != nil {
		t.Error(err)
	}

	c1buf := bytes.NewBuffer(nil)
	err = repo1.Git(ctx, nil, c1buf, "rev-parse", "HEAD")
	if err != nil {
		t.Error(err)
	}

	c1 := strings.TrimSpace(c1buf.String())

	err = repo1.Git(ctx, nil, nil, "checkout", c0)
	if err != nil {
		t.Error(err)
	}

	newContent, err := ioutil.ReadFile(fpath)
	if err != nil {
		t.Error(err)
	}

	if !bytes.Equal(originalContent.Bytes(), newContent) {
		t.Error("after checkout, file content should be equal to content before edit")
	}

	scanbuf := bytes.NewBuffer(nil)
	err = repo1.Scan(c0, c1, scanbuf)
	if err != nil {
		t.Error(err)
	}

	if len(scanbuf.Bytes())%(hex.EncodedLen(bits.KeySize)+1) != 0 {
		t.Errorf("expected a multitude keys to be returned but got: %s", scanbuf.String())
	}
}

//tests pushing and fetching objects from a git remote
func TestPushFetch(t *testing.T) {
	ctx := context.Background()
	ctx, _ = context.WithTimeout(ctx, time.Second*60)

	remote1 := GitInitRemote(t)
	wd1, repo1 := GitCloneWorkspace(remote1, t)
	WriteGitAttrFile(t, wd1, map[string]string{
		"*.bin": "filter=bits",
	})

	bucket := os.Getenv("TEST_BUCKET")
	if bucket == "" {
		t.Errorf("env TEST_BUCKET not configured")
	}

	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	if accessKey == "" {
		t.Errorf("env AWS_ACCESS_KEY_ID not configured")
	}

	secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	if secretKey == "" {
		t.Errorf("env AWS_SECRET_ACCESS_KEY not configured")
	}

	conf := bits.DefaultConf()
	conf.AWSS3BucketName = bucket
	conf.AWSAccessKeyID = accessKey
	conf.AWSSecretAccessKey = secretKey

	err := repo1.Init(os.Stderr, conf)
	if err != nil {
		t.Error(err)
	}

	fsize := int64(5 * 1024 * 1024)
	fpath := filepath.Join(wd1, " with space.bin")
	f1 := WriteRandomFile(t, fpath, fsize)
	f1.Close()

	err = repo1.Git(ctx, nil, nil, "add", "-A")
	if err != nil {
		t.Fatal(err)
	}

	err = repo1.Git(ctx, nil, nil, "commit", "-m", "base")
	if err != nil {
		t.Fatal(err)
	}

	//Push 1
	err = repo1.Git(ctx, nil, nil, "push")
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		func() {
			f, err := os.OpenFile(fpath, os.O_RDWR, 0666)
			if err != nil {
				t.Fatal(err)
			}

			defer f.Close()
			pos := mrand.Int63n(fsize)
			_, err = f.WriteAt([]byte{0x01}, pos)
			if err != nil {
				t.Fatal(err)
			}

			err = repo1.Git(ctx, nil, nil, "add", "-A")
			if err != nil {
				t.Fatal(err)
			}

			err = repo1.Git(ctx, nil, nil, "commit", "-m", fmt.Sprintf("c%d", i))
			if err != nil {
				t.Fatal(err)
			}
		}()
	}

	orgContent, err := ioutil.ReadFile(filepath.Join(wd1, " with space.bin"))
	if err != nil {
		t.Error(err)
	}

	//Push 2
	err = repo1.Git(ctx, nil, nil, "push")
	if err != nil {
		t.Fatal(err)
	}

	wd2, repo2 := GitCloneWorkspace(remote1, t)
	WriteGitAttrFile(t, wd2, map[string]string{
		"*.bin": "filter=bits",
	})

	err = repo2.Init(os.Stderr, conf)
	if err != nil {
		t.Error(err)
	}

	newContent, err := ioutil.ReadFile(filepath.Join(wd2, " with space.bin"))
	if err != nil {
		t.Error(err)
	}

	if !bytes.Equal(orgContent, newContent) {
		t.Error("after clone and init, file content should be equal to content before edit")
	}
}

package bits_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
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

func GitCloneWorkspace(remote string, t *testing.T) (dir string, repo *bits.GitRepository) {
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

	repo, err = bits.NewGitRepository(dir)
	if err != nil {
		t.Fatal(err)
	}

	return dir, repo
}

func TestNewRepository(t *testing.T) {
	_, err := bits.NewGitRepository("/tmp/my-bogus-repo")
	if err == nil {
		t.Errorf("creating repo in non-existing directory should fail")
	} else {
		if !strings.Contains(err.Error(), "no such file") {
			t.Errorf("creating repo should fail with non existing dir error, got: %v", err)
		}
	}

	tdir, _ := ioutil.TempDir("", "test_wdir_")
	_, err = bits.NewGitRepository(tdir)
	if err == nil {
		t.Errorf("creating repo in non-git directory should fail")
	} else {
		if !strings.Contains(err.Error(), "exit status") {
			t.Errorf("creating repo should fail with exit code, got: %v", err)
		}
	}

}

func TestGitIndexSaveLoad(t *testing.T) {
	ctx := context.Background()
	ctx, _ = context.WithTimeout(ctx, time.Second)

	remote1 := GitInitRemote(t)
	_, repo1 := GitCloneWorkspace(remote1, t)

	var err error
	var idx1 bits.SharedIndex
	idx1, err = bits.NewGitIndex(repo1, "", "")
	if err != nil {
		t.Fatal(err)
	}

	out := bytes.NewBuffer(nil)
	err = repo1.Git(ctx, nil, out, "show-ref", bits.DefaultIndexBranch)
	if err == nil {
		t.Error("show-ref should fail, branch non existing")
	}

	k1 := sha256.Sum256([]byte("my-key-1"))
	k2 := sha256.Sum256([]byte("my-key-2"))
	k3 := sha256.Sum256([]byte("my-key-3"))
	idx1.Add(k1)
	idx1.Add(k2)
	if ok, _ := idx1.Has(k1); !ok {
		t.Error("expected index to have key 1")
	}

	if ok, _ := idx1.Has(k2); !ok {
		t.Error("expected index to have key 2")
	}

	err = idx1.Save(ctx)
	if err != nil {
		t.Error(err)
	}

	idx1.Add(k3)
	if ok, _ := idx1.Has(k3); !ok {
		t.Error("expected index to have key 3")
	}

	out = bytes.NewBuffer(nil)
	err = repo1.Git(ctx, nil, out, "show-ref", bits.DefaultIndexBranch)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "refs/heads/bits_chunk_idx") {
		t.Error(fmt.Sprintf("should show the index object"))
	}

	err = idx1.Load(ctx)
	if err != nil {
		t.Error(err)
	}

	if ok, _ := idx1.Has(k1); !ok {
		t.Error("expected index to have key 1")
	}

	if ok, _ := idx1.Has(k2); !ok {
		t.Error("expected index to have key 2")
	}

	if ok, _ := idx1.Has(k3); ok {
		t.Error("expected index to not have key 3")
	}
}

func TestGitIndexPushPull(t *testing.T) {
	ctx := context.Background()
	ctx, _ = context.WithTimeout(ctx, time.Second)

	remote1 := GitInitRemote(t)
	_, repo1 := GitCloneWorkspace(remote1, t)
	_, repo2 := GitCloneWorkspace(remote1, t)

	var err error
	var idx1 bits.SharedIndex
	var idx2 bits.SharedIndex
	idx1, err = bits.NewGitIndex(repo1, "", "origin")
	if err != nil {
		t.Fatal(err)
	}

	idx2, err = bits.NewGitIndex(repo2, "", "origin")
	if err != nil {
		t.Fatal(err)
	}

	k1 := sha256.Sum256([]byte("my-key-1"))
	k2 := sha256.Sum256([]byte("my-key-2"))
	k3 := sha256.Sum256([]byte("my-key-3"))
	idx1.Add(k1)
	idx1.Add(k2)
	idx2.Add(k3)

	err = idx1.Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	err = idx1.Push(ctx)
	if err != nil {
		t.Error(err)
	}

	err = idx2.Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	err = idx2.Pull(ctx)
	if err != nil {
		t.Error(err)
	}

	err = idx2.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if ok, _ := idx2.Has(k1); !ok {
		t.Error("expected index to have key 1")
	}

	if ok, _ := idx2.Has(k2); !ok {
		t.Error("expected index to have key 2")
	}

	if ok, _ := idx2.Has(k3); !ok {
		t.Error("expected index to have key 3")
	}

}

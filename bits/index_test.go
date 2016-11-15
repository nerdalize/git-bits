package bits_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nerdalize/git-bits/bits"
)

func TestGitIndexSaveLoad(t *testing.T) {
	ctx := context.Background()
	ctx, _ = context.WithTimeout(ctx, time.Second)

	remote1 := GitInitRemote(t)
	_, repo1 := GitCloneWorkspace(remote1, t)

	var err error
	idx1, err := bits.NewIndex(repo1, "", "")
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
	idx1, err := bits.NewIndex(repo1, "", "origin")
	if err != nil {
		t.Fatal(err)
	}

	idx2, err := bits.NewIndex(repo2, "", "origin")
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

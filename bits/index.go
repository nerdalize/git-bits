package bits

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"io"
	"strings"
)

var (
	//DefaultIndexBranch is the name of the branch the GitIndex uses to store keys
	DefaultIndexBranch = "refs/heads/bits_chunk_idx"

	//DefaultCommitMessage is the commit message written for index updates
	DefaultCommitMessage = "chunk index updated"
)

//Index stores chunk keys in a specialty branch of a Git repository
//this branch can be shared by users to give others access (and knowledge)
//of file chunks.
type Index struct {

	//interface into the git repository this index is located in
	repo *Repository

	//full name (refs/heads/...) of the local branch the index saves and loads from
	branch string

	//git remote name to which an index is pushed and pulled
	remote string

	//unbound set of chunk keys
	set map[K]interface{}
}

//NewIndex will create a SharedIndex from a branch in the provided git
//repository that can be pushed and pulled
func NewIndex(repo *Repository, branch, remote string) (idx *Index, err error) {
	if branch == "" {
		branch = DefaultIndexBranch
	}

	refsPrefix := "refs/heads/"
	if !strings.HasPrefix(branch, refsPrefix) {
		return nil, fmt.Errorf("index branch '%s' must be provided as a full ref name: it doesnt start with '%s' ", branch, refsPrefix)
	}

	idx = &Index{
		repo:   repo,
		branch: branch,
		remote: remote,
	}

	return idx, idx.Clear()
}

//Has will return true if the given key can be found in the current
//memory representation of the git index, if it cannot be found it could
//mean the chunk doesnt exist, is not yet loaded from our specialty branch
//or still resides in a remote index and needs to be pulled
func (idx *Index) Has(k K) (b bool, err error) {
	_, ok := idx.set[k]
	return ok, nil
}

//Add a key to the in-memory representation, it order to share this key
//will first need to be saved to the Git database and then be pushed
//to a git remote the other users can fetch from
func (idx *Index) Add(k K) (err error) {
	idx.set[k] = nil
	return nil
}

//Serialize the Git index in-memory representation
func (idx *Index) Serialize(w io.Writer) (err error) {
	enc := gob.NewEncoder(w)
	return enc.Encode(idx.set)
}

//Deserialize and overwrite the in-memory representation
func (idx *Index) Deserialize(r io.Reader) (err error) {
	err = idx.Clear()
	if err != nil {
		return err
	}

	dec := gob.NewDecoder(r)
	return dec.Decode(&idx.set)
}

func (idx *Index) updateBranchCommit(ctx context.Context, sha1 string) (err error) {
	return idx.repo.Git(ctx, nil, nil, "update-ref", idx.branch, sha1)
}

func (idx *Index) readCommit(ctx context.Context, sha1 string, w io.Writer) (err error) {
	return idx.repo.Git(ctx, nil, w, "show", fmt.Sprintf("%s:remote.cidx", sha1))
}

func (idx *Index) writeCommit(ctx context.Context, parentsSha1 ...string) (sha1 string, err error) {
	in := bytes.NewBuffer(nil)
	err = idx.Serialize(in)
	if err != nil {
		return "", fmt.Errorf("failed to serialize index: %v", err)
	}

	out := bytes.NewBuffer(nil)
	err = idx.repo.Git(ctx, in, out, "hash-object", "--stdin", "-w")
	if err != nil {
		return "", err
	}

	blogSha1 := strings.TrimSpace(out.String())
	if blogSha1 == "" {
		return "", fmt.Errorf("hash-object didnt return anything")
	}

	in = bytes.NewBufferString(fmt.Sprintf("100644 blob %s\tremote.cidx", blogSha1))
	out = bytes.NewBuffer(nil)
	err = idx.repo.Git(ctx, in, out, "mktree")
	if err != nil {
		return "", err
	}

	treeSha1 := strings.TrimSpace(out.String())
	if treeSha1 == "" {
		return "", fmt.Errorf("mktree didnt return anything")
	}

	in = bytes.NewBufferString(DefaultCommitMessage)
	out = bytes.NewBuffer(nil)
	args := []string{"commit-tree", treeSha1}
	for _, parentSha1 := range parentsSha1 {
		args = append(args, "-p", parentSha1)
	}

	err = idx.repo.Git(ctx, in, out, args...)
	if err != nil {
		return "", err
	}

	sha1 = strings.TrimSpace(out.String())
	if sha1 == "" {
		return "", fmt.Errorf("commit-tree didnt return anything")
	}

	return sha1, nil
}

func (idx *Index) showBranchCommit(ctx context.Context) (sha1 string, err error) {
	out := bytes.NewBuffer(nil)
	err = idx.repo.Git(ctx, nil, out, "show-ref", "-s", idx.branch)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out.String()), nil
}

//Save will perisst the in-memory representation to the Git database
func (idx *Index) Save(ctx context.Context) (err error) {
	c1, err := idx.showBranchCommit(ctx)
	if err != nil && !strings.Contains(err.Error(), "exit status 1") {
		//'exit status 1' means the branch doesnt exist, thats OK it will be
		//created in in an update-ref call later on
		return fmt.Errorf("failed to get branch commit: %v", err)
	}

	var c2 string
	if c1 == "" {
		c2, err = idx.writeCommit(ctx)
	} else {
		c2, err = idx.writeCommit(ctx, c1)
	}

	if err != nil {
		return fmt.Errorf("failed to write index commit: %v", err)
	}

	err = idx.updateBranchCommit(ctx, c2)
	if err != nil {
		return fmt.Errorf("failed to update index branch: %v", err)
	}

	return nil
}

//Load will overwrite the in-memory representation with the contents
//from the Git database
func (idx *Index) Load(ctx context.Context) (err error) {
	sha1, err := idx.showBranchCommit(ctx)
	if err != nil || sha1 == "" {
		return nil //nothing to load
	}

	buf := bytes.NewBuffer(nil)
	err = idx.readCommit(ctx, sha1, buf)
	if err != nil {
		return fmt.Errorf("failed to read commit '%s' for index: %v", sha1, err)
	}

	err = idx.Deserialize(buf)
	if err != nil {
		return fmt.Errorf("failed to deserialize index: %v", err)
	}

	return nil
}

//Pull will fetch and merge a remote index with the local branch,
//it does not immediately update the in-memory representation
func (idx *Index) Pull(ctx context.Context) (err error) {
	if idx.remote == "" {
		return fmt.Errorf("index wasnt configured with a remote to push and pull from: %v", err)
	}

	err = idx.repo.Git(ctx, nil, nil, "fetch", idx.remote, fmt.Sprintf("%s:%s", idx.branch, idx.branch))
	if err != nil {
		if !strings.Contains(err.Error(), "exit status 1") {
			return fmt.Errorf("unexpected fetch error: %v", err)
		}

		//assume exist status 1 means we couldnt fast forward, FETCH_HEAD
		//should contain a ref to the commit that was fetched, we continue
		//with the creation of a custom commit that merges the current branch
		//with the newly fetched head
		//
		//@TODO the current merge/save/load setup is dangerous, it seems pretty
		//likely some data will get lost in race conditions between disk (Git db)
		//and im-memory representation. this needs to be tested more

		out := bytes.NewBuffer(nil)
		err = idx.repo.Git(ctx, nil, out, "rev-parse", "FETCH_HEAD")
		if err != nil {
			return fmt.Errorf("failed to parse fetched head: %v", err)
		}

		newHeadSha1 := strings.TrimSpace(out.String())
		if newHeadSha1 == "" {
			return fmt.Errorf("couldnt parse fetched head to commit sha1")
		}

		oldHeadSha1, err := idx.showBranchCommit(ctx)
		if err != nil {
			return fmt.Errorf("coudnt get idex branch commit: %v", err)
		}

		newHeadBuf := bytes.NewBuffer(nil)
		err = idx.readCommit(ctx, newHeadSha1, newHeadBuf)
		if err != nil {
			return fmt.Errorf("failed to read new head commit: %v", err)
		}

		oldHeadBuf := bytes.NewBuffer(nil)
		err = idx.readCommit(ctx, oldHeadSha1, oldHeadBuf)
		if err != nil {
			return fmt.Errorf("failed to read old head commit: %v", err)
		}

		newSet := map[K]interface{}{}
		newSetDec := gob.NewDecoder(newHeadBuf)
		err = newSetDec.Decode(&newSet)
		if err != nil {
			return fmt.Errorf("failed to decode new head: %v", err)
		}

		oldSet := map[K]interface{}{}
		oldSetDec := gob.NewDecoder(oldHeadBuf)
		err = oldSetDec.Decode(&oldSet)
		if err != nil {
			return fmt.Errorf("failed to decode old head: %v", err)
		}

		tmpIndx, err := NewIndex(idx.repo, idx.branch, idx.remote)
		if err != nil {
			return fmt.Errorf("failed to setup tmp git index: %v", err)
		}

		for k := range oldSet {
			err = tmpIndx.Add(k)
			if err != nil {
				return fmt.Errorf("failed to merge key '%x' (old set): %v", k, err)
			}
		}

		for k := range newSet {
			err = tmpIndx.Add(k)
			if err != nil {
				return fmt.Errorf("failed to merge key '%x' (new set): %v", k, err)
			}
		}

		c3, err := tmpIndx.writeCommit(ctx, oldHeadSha1, newHeadSha1)
		if err != nil {
			return fmt.Errorf("failed to write merged commit: %v", err)
		}

		err = idx.updateBranchCommit(ctx, c3)
		if err != nil {
			return fmt.Errorf("updated index branch commit: %v", err)
		}
	}

	return nil
}

//Push will send the contents of the local index branch to a Git remote
//such that other users can pull and merge to gain knowledge of newly
//uploaded chunks
func (idx *Index) Push(ctx context.Context) (err error) {
	if idx.remote == "" {
		return fmt.Errorf("index wasnt configured with a remote to push and pull from: %v", err)
	}

	return idx.repo.Git(ctx, nil, nil, "push", idx.remote, fmt.Sprintf("%s:%s", idx.branch, idx.branch))
}

//Clear will whipe the in-memory representation of the index
func (idx *Index) Clear() (err error) {
	idx.set = map[K]interface{}{}
	return nil
}

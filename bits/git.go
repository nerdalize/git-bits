package bits

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//GitRepository provides an abstraction on top of a Git repository in a
//certain directory that is queried by git commands
type GitRepository struct {
	//Path the to the Git executable we're usng
	exe string

	//Path to the Git repository we're operating in
	dir string

	//Git stderr from executions will be written here
	errOutput io.Writer
}

//NewGitRepository sets up a Git interface to a repository in the
//provdided directory. It will fail if the get executable is not in
//the shells PATH or if the directory doesnt seem to be a Git repository
func NewGitRepository(dir string) (repo *GitRepository, err error) {
	repo = &GitRepository{}
	repo.exe, err = exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("git executable couldn't be found in your PATH: %v, make sure git it installed", err)
	}

	repo.dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to turn repository path '%s' into an absolute path: %v", dir, err)
	}

	//@TODO make this configurable
	repo.errOutput = os.Stderr

	err = repo.Git(nil, nil, nil, "status")
	if err != nil {
		return nil, fmt.Errorf("couldn't exec git status: %v", err)
	}

	return repo, nil
}

//Git runs the git executable with the working directory set to the repository director
func (r *GitRepository) Git(ctx context.Context, in io.Reader, out io.Writer, args ...string) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}

	cmd := exec.CommandContext(ctx, r.exe, args...)
	cmd.Dir = r.dir
	cmd.Stderr = r.errOutput
	cmd.Stdin = in
	cmd.Stdout = out

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to run `git %v`: %v", strings.Join(args, " "), err)
	}

	return nil
}

//GetPushedKeys is a high level command that is used in the pre-push hook to
//fetch all chunk keys that are being pushed by Git. The (stile encoded) keys
//are written to writer 'w'
func (r *GitRepository) GetPushedKeys(localSha1 string, remoteSha1 string, w io.Writer) (err error) {
	return fmt.Errorf("not yet implemented")
}

//We use Git for facilitating a shared chunk index
func (r *GitRepository) GetIndexStore() (idx SharedIndex, err error) {
	return idx, fmt.Errorf("not yet implemented")
}

//GitIndex store stores chunk keys in a specialty branch of a Git repository
//this branch can be shared by users to give others access (and knowledge)
//of file chunks.
type GitIndex struct {

	//interface into the git repository this index is located in
	repo *GitRepository

	//full name (refs/heads/...) of the local branch the index saves and loads from
	branch string

	//unbound set of chunk keys
	set map[K]interface{}
}

var (
	//DefaultIndexBranch is the name of the branch the GitIndex uses to store keys
	DefaultIndexBranch = "refs/heads/bits_chunk_idx"

	//DefaultCommitMessage is the commit message written for index updates
	DefaultCommitMessage = "chunk index updated"
)

//NewGitIndex will create a SharedIndex from a branch in the provided git
//repository that can be pushed and pulled
func NewGitIndex(repo *GitRepository, branch string) (idx *GitIndex, err error) {
	if branch == "" {
		branch = DefaultIndexBranch
	}

	refsPrefix := "refs/heads/"
	if !strings.HasPrefix(branch, refsPrefix) {
		return nil, fmt.Errorf("index branch '%s' must be provided as a full ref name: it doesnt start with '%s' ", branch, refsPrefix)
	}

	idx = &GitIndex{
		repo:   repo,
		branch: branch,
	}

	return idx, idx.Clear()
}

//Has will return true if the given key can be found in the current
//memory representation of the git index, if it cannot be found it could
//mean the chunk doesnt exist, is not yet loaded from our specialty branch
//or still resides in a remote index and needs to be pulled
func (idx *GitIndex) Has(k K) (b bool, err error) {
	_, ok := idx.set[k]
	return ok, nil
}

//Add a key to the in-memory representation, it order to share this key
//will first need to be saved to the Git database and then be pushed
//to a git remote the other users can fetch from
func (idx *GitIndex) Add(k K) (err error) {
	idx.set[k] = nil
	return nil
}

//Serialize the Git index in-memory representation
func (idx *GitIndex) Serialize(w io.Writer) (err error) {
	enc := gob.NewEncoder(w)
	return enc.Encode(idx.set)
}

//Deserialize and overwrite the in-memory representation
func (idx *GitIndex) Deserialize(r io.Reader) (err error) {
	err = idx.Clear()
	if err != nil {
		return err
	}

	dec := gob.NewDecoder(r)
	return dec.Decode(&idx.set)
}

func (idx *GitIndex) updateBranchCommit(ctx context.Context, sha1 string) (err error) {
	return idx.repo.Git(ctx, nil, nil, "update-ref", idx.branch, sha1)
}

func (idx *GitIndex) readCommit(ctx context.Context, sha1 string, w io.Writer) (err error) {
	return idx.repo.Git(ctx, nil, w, "show", fmt.Sprintf("%s:remote.cidx", sha1))
}

func (idx *GitIndex) writeCommit(ctx context.Context, parentsSha1 ...string) (sha1 string, err error) {
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

func (idx *GitIndex) showBranchCommit(ctx context.Context) (sha1 string, err error) {
	out := bytes.NewBuffer(nil)
	err = idx.repo.Git(ctx, nil, out, "show-ref", "-s", idx.branch)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out.String()), nil
}

//Save will perisst the in-memory representation to the Git database
func (idx *GitIndex) Save(ctx context.Context) (err error) {
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
func (idx *GitIndex) Load(ctx context.Context) (err error) {
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

//Pull will fetch and merge a remote index branch into the local branch
//the newly merged index needs to be loaded manually afterwards
func (idx *GitIndex) Pull(ctx context.Context) (err error) {
	// fetch <remote> <branch>
	// rev-parse FETCH_HEAD
	// show-rev
	// read-commit <left>
	// read-commit <right>
	// [MERGE]
	// write-commit
	// update-ref

	return fmt.Errorf("not yet implemented")
}

//Push will send the contents of the local index branch to a Git remote
//such that other users can pull and merge to gain knowledge of newly
//uploaded chunks
func (idx *GitIndex) Push(ctx context.Context) (err error) {
	return fmt.Errorf("not yet implemented")
}

//Clear will whipe the in-memory representation of the index
func (idx *GitIndex) Clear() (err error) {
	idx.set = map[K]interface{}{}
	return nil
}

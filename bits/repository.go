package bits

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/restic/chunker"
)

var (
	//ChunkBufferSize determines the size of the buffer that wil hold each chunk
	ChunkBufferSize = 8 * 1024 * 1024 //8MiB

	//ChunkPolynomial determines the location of splitting, needs to be equal across de-duplication space
	ChunkPolynomial = chunker.Pol(0x3DA3358B4DC173)
)

//Repository provides an abstraction on top of a Git repository for a
//certain directory that is queried by git commands
type Repository struct {
	//Path the to the Git executable we're usng
	exe string

	//Path to the Git workspace we're operating in
	workDir string

	//Path to the local chunk storage
	chunkDir string

	//Git stderr from executions will be written here
	errOutput io.Writer

	//A internal database for storing chunk meta information
	db *DB
}

//NewRepository sets up an interface on top of a Git repository in the
//provided directory. It will fail if the get executable is not in
//the shells PATH or if the directory doesnt seem to be a Git repository
func NewRepository(dir string) (repo *Repository, err error) {
	repo = &Repository{}
	repo.exe, err = exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("git executable couldn't be found in your PATH: %v, make sure git it installed", err)
	}

	repo.workDir, err = filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to turn repository path '%s' into an absolute path: %v", dir, err)
	}

	//@TODO make this configurable
	repo.errOutput = os.Stderr

	_, err = os.Stat(filepath.Join(repo.workDir, ".git"))
	if err != nil {
		return nil, fmt.Errorf("dir '%s' doesnt seem to be a git workspace:  %v", dir, err)
	}

	//@TODO make this configurable
	repo.chunkDir = filepath.Join(repo.workDir, ".git", "chunks")
	err = os.MkdirAll(repo.chunkDir, 0777)
	if err != nil {
		return nil, fmt.Errorf("couldnt setup chunk directory at '%s': %v", repo.chunkDir, err)
	}

	logp := filepath.Join(repo.chunkDir, fmt.Sprintf("%x.cleaned", sha1.Sum([]byte(repo.chunkDir))))
	repo.db, err = NewDB(logp)
	if err != nil {
		return nil, fmt.Errorf("failed to open bits database at '%s': %v", logp, err)
	}

	return repo, nil
}

//Git runs the git executable with the working directory set to the repository director
func (repo *Repository) Git(ctx context.Context, in io.Reader, out io.Writer, args ...string) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}

	cmd := exec.CommandContext(ctx, repo.exe, args...)
	cmd.Dir = repo.workDir
	cmd.Stderr = repo.errOutput
	cmd.Stdin = in
	cmd.Stdout = out

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to run `git %v`: %v", strings.Join(args, " "), err)
	}

	return nil
}

//Clean turns a plain bytes from 'r' into encrypted, deduplicated and persisted chunks
//while outputting keys for those chunks on writer 'w'. Chunks are written to a local chunk
//space, pushing these to a remote store happens at a later time (pre-push hook) but a log
//of key file blob hashes is kept to recognize them during a push.
func (repo *Repository) Clean(r io.Reader, w io.Writer) (err error) {
	blob := bytes.NewBuffer(nil)
	out := io.MultiWriter(w, blob)

	//start chunking
	chunkr := chunker.New(r, ChunkPolynomial)
	buf := make([]byte, ChunkBufferSize)
	for {
		chunk, err := chunkr.Next(buf)
		if err == io.EOF {
			break
		}

		if err != nil {
			return fmt.Errorf("Failed to write chunk (%d bytes) to buffer (size %d bytes): %v", chunk.Length, ChunkBufferSize, err)
		}

		k := sha256.Sum256(chunk.Data)
		printk := func(k K) error {
			_, err = fmt.Fprintf(out, "%x\n", k)
			if err != nil {
				return fmt.Errorf("failed to write key to output: %v", err)
			}

			return nil
		}

		err = func() error {

			//@TODO encrypt chunks

			//setup chunk directory
			dir := filepath.Join(repo.chunkDir, fmt.Sprintf("%x", k[:2]))
			err = os.MkdirAll(dir, 0777)
			if err != nil {
				return fmt.Errorf("failed to create chunk dir '%s': %v", dir, err)
			}

			//open chunk, if already exists nothing to write
			p := filepath.Join(dir, fmt.Sprintf("%x", k[2:]))
			f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0666)
			if err != nil {
				if os.IsExist(err) {
					//already writen, all good; output key
					return printk(k)
				}

				return fmt.Errorf("Failed to open chunk file '%s' for writing: %v", p, err)
			}

			//write chunk file
			defer f.Close()
			n, err := f.Write(chunk.Data)
			if err != nil {
				return fmt.Errorf("Failed to write chunk '%x' (wrote %d bytes): %v", k, n, err)
			}

			//output key
			return printk(k)
		}()

		if err != nil {
			return fmt.Errorf("Failed to split chunk '%x': %v", k, err)
		}
	}

	//hash the blob with key as git would hash it
	blobHash := sha1.New()
	_, err = fmt.Fprintf(blobHash, "blob %d", blob.Len())
	if err != nil {
		return fmt.Errorf("failed to write keys for blob hash: %v", err)
	}

	_, err = blobHash.Write([]byte{0x00})
	if err != nil {
		return fmt.Errorf("failed to write keys for blob hash: %v", err)
	}

	_, err = io.Copy(blobHash, blob)
	if err != nil {
		return fmt.Errorf("failed to write keys for blob hash: %v", err)
	}

	//log each blob that ever resulted from a clean, this can later
	//be used to figure out if a git object contains keys without looking
	//at the blob content
	err = repo.db.LogClean(blobHash.Sum(nil))
	if err != nil {
		return fmt.Errorf("failed to log clean: %v", err)
	}

	return nil
}

//Smudge turns a newline seperated list of chunk keys from 'r' and lazily fetches each
//chunk from the local space - or if not present locally - from a remote store. Chunks
//are then decrypted and combined in the original file and written to writer 'w'
func (repo *Repository) Smudge(r io.Reader, w io.Writer) (err error) {
	s := bufio.NewScanner(r)
	for s.Scan() {
		data := make([]byte, hex.DecodedLen(len(s.Bytes())))
		_, err = hex.Decode(data, s.Bytes())
		if err != nil {
			return fmt.Errorf("failed to decode '%x' as hex: %v", s.Bytes(), err)
		}

		k := K{}
		if len(k) != len(data) {
			return fmt.Errorf("decoded chunk key '%x' has an invalid lenght %d, expected %d", data, len(data), len(k))
		}

		copy(k[:], data[:32])
		err = func() error {

			//open chunk file
			p := filepath.Join(repo.chunkDir, fmt.Sprintf("%x", k[:2]), fmt.Sprintf("%x", k[2:]))
			f, err := os.OpenFile(p, os.O_RDONLY, 0666)
			if err != nil {
				return fmt.Errorf("failed to open chunk '%x' at '%s': %v", k, p, err)
			}

			//@TODO decrypt chunk

			//copy chunk bytes to output
			defer f.Close()
			n, err := io.Copy(w, f)
			if err != nil {
				return fmt.Errorf("failed to copy chunk '%x' content after %d bytes: %v", k, n, err)
			}

			fmt.Fprintf(os.Stderr, "key %x", k)
			return nil
		}()

		if err != nil {
			return fmt.Errorf("Failed to combine chunk '%x': %v", k, err)
		}
	}

	if err = s.Err(); err != nil {
		return fmt.Errorf("failed to scan smudge input: %v", err)
	}

	return nil
}

//GetPushedKeys is a high level command that is used in the pre-push hook to
//fetch all chunk keys that are being pushed by Git. The (still encoded) keys
//are written to writer 'w'
//
// @TODO there are some issues here: 1) it currently involves doing an ERROR PRONE walking
// of git objects with a method that may or may not actually walk all objects and
// 2) while needing to large files into memory without knowing if they will be
// of any use, git-lfs can cut off based on size, we CANNOT. 3) it ties push logic very
// closely to git.
func (repo *Repository) GetPushedKeys(ctx context.Context, localSha1 string, remoteSha1 string, w io.Writer) (err error) {
	// objs := bytes.NewBuffer(nil)
	// err = r.Git(ctx, nil, objs, "rev-list", "--objects", "--all", localSha1, "^"+remoteSha1)
	// if err != nil {
	// 	return fmt.Errorf("failed to list pushed objects: %v", err)
	// }
	//
	// objSha1s := bytes.NewBuffer(nil)
	// scanner := bufio.NewScanner(objs)
	// for scanner.Scan() {
	// 	fields := bytes.Fields(scanner.Bytes())
	// 	if len(fields) < 1 {
	// 		return fmt.Errorf("unexpected rev-list line '%s': expected at least 1 fields", string(scanner.Text()))
	// 	}
	//
	// 	_, err = objSha1s.Write(fields[0])
	// 	_, err = objSha1s.WriteString("\n")
	// 	if err != nil {
	// 		return fmt.Errorf("failed to write object sha to buffer: %v", err)
	// 	}
	// }
	//
	// if err = scanner.Err(); err != nil {
	// 	return fmt.Errorf("failed to scan rev-list output: %v", err)
	// }
	//
	// checks := bytes.NewBuffer(nil)
	// err = r.Git(ctx, objSha1s, checks, "cat-file", "--batch-check")
	// if err != nil {
	// 	return fmt.Errorf("failed to list pushed objects: %v", err)
	// }
	//
	// blobs := bytes.NewBuffer(nil)
	// scanner = bufio.NewScanner(checks)
	// for scanner.Scan() {
	// 	fields := bytes.Fields(scanner.Bytes())
	// 	if len(fields) < 3 {
	// 		return fmt.Errorf("unexpected cat-file line '%s': expected at least 3 fields", string(scanner.Text()))
	// 	}
	//
	// 	if !bytes.Equal(fields[1], []byte("blob")) {
	// 		continue
	// 	}
	//
	// 	objSize, err := strconv.ParseInt(string(fields[2]), 10, 64)
	// 	if err != nil {
	// 		return fmt.Errorf("unexpected size from cat-file could not parsed as int: %v", err)
	// 	}
	//
	// 	//objects smaller then 32 bytes cannot contain hashes
	// 	if objSize < 32 {
	// 		continue
	// 	}
	//
	// 	//index files are always a set of newline seperated 32byte hashes,
	// 	//as such the object size must be multitude of 33 bytes this isnt very
	// 	//flexible but should prevent most blobs from being loaded into memory
	// 	//
	// 	//@TODO this isnt very flexible. INSTEAD read from the keys log file
	// 	//that is build up during clean/smudge to see what objects made it into
	// 	//the git database.
	// 	if objSize > 0 && objSize%33 != 0 {
	// 		continue
	// 	}
	//
	// 	fmt.Println(scanner.Text())
	// 	_, err = blobs.Write(fields[0])
	// 	_, err = blobs.WriteString("\n")
	// 	if err != nil {
	// 		return fmt.Errorf("failed to write blob sha to buffer: %v", err)
	// 	}
	// }
	//
	// if err = scanner.Err(); err != nil {
	// 	return fmt.Errorf("failed to scan for blob objects: %v", err)
	// }

	return fmt.Errorf("not yet implemented")
}

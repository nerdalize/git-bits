package bits

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

	//Header key allows us to recognize the start of a key listing
	header []byte

	//Footer Key allows us to recognize the end of a key listing
	footer []byte

	//remotes hold the remote chunk stores we're using
	remotes []Remote
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

	//setup header and footers
	repo.header = []byte("-------------------------CHUNKS_START---------------------------\n")
	repo.footer = []byte("--------------------------CHUNKS_END----------------------------\n")
	if len(repo.header) != (hex.EncodedLen(KeySize)+1) || len(repo.footer) != (hex.EncodedLen(KeySize)+1) {
		return nil, fmt.Errorf("repository header and footer size are not '%d': header: %d, footer: %d", hex.EncodedLen(KeySize)+1, len(repo.header), len(repo.footer))
	}

	//@TODO make "origin" remote configurable
	def, err := NewS3Remote(repo, "origin")
	if err != nil {
		return nil, fmt.Errorf("unable to setup default chunk remote: %v", err)
	}

	repo.remotes = append(repo.remotes, def)
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

//Initializes a repository for git bits
// configure filter
// install hook
// replace pointers?
func (repo *Repository) Init() (err error) {
	return nil
}

//Push takes a list of chunk keys on reader 'r' and moves each chunk from
//the local storage to the remote store with name 'remote'.
func (repo *Repository) Push(r io.Reader, remoteName string) (err error) {

	//get the remote we're interested in
	var remote Remote
	available := []string{}
	for _, r := range repo.remotes {
		available = append(available, r.Name())
		if r.Name() == remoteName {
			remote = r
		}
	}

	if remote == nil {
		return fmt.Errorf("no remote in repository named '%s', available %v", remoteName, available)
	}

	//scan for chunk keys
	indexed := []K{}
	s := bufio.NewScanner(r)
	for s.Scan() {

		//we skip header and footer lines
		if bytes.Equal(s.Bytes(), repo.header[:len(repo.header)-1]) || bytes.Equal(s.Bytes(), repo.footer[:len(repo.footer)-1]) {
			continue
		}

		//decode actual keys
		data := make([]byte, hex.DecodedLen(len(s.Bytes())))
		_, err = hex.Decode(data, s.Bytes())
		if err != nil {
			return fmt.Errorf("failed to decode '%x' as hex: %v", s.Bytes(), err)
		}

		k := K{}
		if len(k) != len(data) {
			return fmt.Errorf("decoded chunk key '%x' has an invalid lenght %d, expected %d", data, len(data), len(k))
		}

		copy(k[:], data[:KeySize])
		err = func() error {

			//check if remote has the key
			ok, err := remote.Index().Has(k)
			if err != nil {
				return fmt.Errorf("failed to check remote index: %v", err)
			}

			if ok {
				return nil //skip push
			}

			//open local chunk file
			p := filepath.Join(repo.chunkDir, fmt.Sprintf("%x", k[:2]), fmt.Sprintf("%x", k[2:]))
			f, err := os.OpenFile(p, os.O_RDONLY, 0666)
			if err != nil {
				return fmt.Errorf("failed to open chunk '%x' at '%s' for pushing: %v", k, p, err)
			}

			//get remote writer
			defer f.Close()
			wc, err := remote.ChunkWriter(k)
			if err != nil {
				return fmt.Errorf("failed to get chunk writer: %v", err)
			}

			//start upload
			defer wc.Close()
			n, err := io.Copy(wc, f)
			if err != nil {
				return fmt.Errorf("failed to copy file '%s' to remote writer after %d bytes: %v", f.Name(), n, err)
			}

			//add key to remote index
			err = remote.Index().Add(k)
			if err != nil {
				return fmt.Errorf("failed to add key '%x' to remote index: %v", err)
			}

			//record number of newly indexed keys
			indexed = append(indexed, k)
			return nil
		}()
	}

	if err = s.Err(); err != nil {
		return fmt.Errorf("failed to scan push input: %v", err)
	}

	//if some new keys were indexed for the remote save
	//and push the index for others to pull
	if len(indexed) > 0 {
		ctx := context.Background()
		err = remote.Index().Save(ctx)
		if err != nil {
			return fmt.Errorf("failed to save index: %v", err)
		}

		err = remote.Index().Push(ctx)
		if err != nil {
			return fmt.Errorf("failed to push ined: %v", err)
		}
	}

	return nil
}

//Fetch takes a list of chunk keys on reader 'r' ...
func (repo *Repository) Fetch(r io.Reader) (err error) {
	//if it exists locally, skip
	//check the remote's index and if its there fetch it

	return fmt.Errorf("not yet implemented")
}

//Scan will traverse git objects between commit 'left' and 'right', it will
//look for blobs larger then 32 bytes that are also in the clean log. These
//blobs should contain keys that are written to writer 'w'
func (repo *Repository) Scan(left, right string, w io.Writer) (err error) {

	// rev-list --objects <right> ^<left> | f1 | cat-file --batch-check | f2 | cat-file --batch | f3
	ctx := context.Background()
	r1, w1 := io.Pipe()
	r2, w2 := io.Pipe()
	r3, w3 := io.Pipe()
	r4, w4 := io.Pipe()
	r5, w5 := io.Pipe()

	errs := []string{}
	errCh := make(chan error)
	defer close(errCh)
	go func() {
		for err := range errCh {
			errs = append(errs, fmt.Sprintf("%v", err))
		}
	}()

	go func() {
		defer w1.Close()
		err = repo.Git(ctx, nil, w1, "rev-list", "--objects", right, "^"+left)
		if err != nil {
			errCh <- err
		}
	}()

	go func() {
		defer w2.Close()
		s := bufio.NewScanner(r1)
		for s.Scan() {
			fields := bytes.Fields(s.Bytes())
			if len(fields) < 1 {
				continue
			}

			fmt.Fprintf(w2, "%s\n", fields[0])
		}

		if err = s.Err(); err != nil {
			errCh <- err
		}
	}()

	go func() {
		defer w3.Close()
		err = repo.Git(ctx, r2, w3, "cat-file", "--batch-check")
		if err != nil {
			errCh <- err
		}
	}()

	go func() {
		defer w4.Close()
		s := bufio.NewScanner(r3)
		for s.Scan() {
			fields := bytes.Fields(s.Bytes())

			//dont consider non-blobs
			if len(fields) < 3 || !bytes.Equal(fields[1], []byte("blob")) {
				continue
			}

			//parse object size for filtering by blob size
			objSize, err := strconv.ParseInt(string(fields[2]), 10, 64)
			if err != nil {
				errCh <- err
				continue
			}

			//all key files have a size that is the exact multiple of
			//33 bytes: 32 bytes hex encoded hashes with a newline character
			if objSize%int64(hex.EncodedLen(KeySize)+1) != 0 {
				continue
			}

			fmt.Fprintf(w4, "%s\n", string(fields[0]))
		}

		if err = s.Err(); err != nil {
			errCh <- err
		}
	}()

	go func() {
		defer w5.Close()
		err = repo.Git(ctx, r4, w5, "cat-file", "--batch")
		if err != nil {
			errCh <- err
		}
	}()

	scanned := map[string]struct{}{}
	recording := false
	s := bufio.NewScanner(r5)
	for s.Scan() {
		if bytes.Equal(s.Bytes(), repo.header[:len(repo.header)-1]) {
			recording = true
			continue
		}

		if bytes.Equal(s.Bytes(), repo.footer[:len(repo.footer)-1]) {
			recording = false
			continue
		}

		//if we found keys, output each key on a new line
		//but only if we didn't output it before
		if recording {
			if _, ok := scanned[s.Text()]; !ok {
				fmt.Fprintf(w, "%s\n", s.Text())
				scanned[s.Text()] = struct{}{}
			}
		}
	}

	if err = s.Err(); err != nil {
		return fmt.Errorf("failed to scan key blobs: %v", err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("there were scanning errors: \n %s", strings.Join(errs, "\n\t"))
	}

	return nil
}

//Split turns a plain bytes from 'r' into encrypted, deduplicated and persisted chunks
//while outputting keys for those chunks on writer 'w'. Chunks are written to a local chunk
//space, pushing these to a remote store happens at a later time (pre-push hook) but a log
//of key file blob hashes is kept to recognize them during a push.
func (repo *Repository) Split(r io.Reader, w io.Writer) (err error) {

	//create a buffer that allows us to peek if this is a file that
	//is already spit, simply copy over the bytes
	bufr := bufio.NewReader(r)
	hdr, _ := bufr.Peek(hex.EncodedLen(KeySize) + 1)
	if bytes.Equal(hdr, repo.header) {
		//@TODO unit test this
		_, err := io.Copy(w, bufr)
		if err != nil {
			return fmt.Errorf("failed to copy already chunked file content: %v", err)
		}

		return nil
	}

	//it is a feel that needs splitting, start
	//writing header and footer
	w.Write(repo.header)
	defer w.Write(repo.footer)

	//write actual chunks
	chunkr := chunker.New(bufr, ChunkPolynomial)
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
			_, err = fmt.Fprintf(w, "%x\n", k)
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

	return nil
}

//Combine turns a newline seperated list of chunk keys from 'r' and lazily fetches each
//chunk from the local space - or if not present locally - from a remote store. Chunks
//are then decrypted and combined in the original file and written to writer 'w'
func (repo *Repository) Combine(r io.Reader, w io.Writer) (err error) {
	s := bufio.NewScanner(r)
	for s.Scan() {

		//we skip header and footer lines
		if bytes.Equal(s.Bytes(), repo.header[:len(repo.header)-1]) || bytes.Equal(s.Bytes(), repo.footer[:len(repo.footer)-1]) {
			continue
		}

		//decode actual keys
		data := make([]byte, hex.DecodedLen(len(s.Bytes())))
		_, err = hex.Decode(data, s.Bytes())
		if err != nil {
			return fmt.Errorf("failed to decode '%x' as hex: %v", s.Bytes(), err)
		}

		k := K{}
		if len(k) != len(data) {
			return fmt.Errorf("decoded chunk key '%x' has an invalid lenght %d, expected %d", data, len(data), len(k))
		}

		copy(k[:], data[:KeySize])
		err = func() error {

			//open chunk file
			p := filepath.Join(repo.chunkDir, fmt.Sprintf("%x", k[:2]), fmt.Sprintf("%x", k[2:]))
			f, err := os.OpenFile(p, os.O_RDONLY, 0666)
			if err != nil {

				//@TODO lookup at each remote and if the chunk is in its index fetch it and try again
				//@TODO if initial lookup failed keep retrying to pull the index until a key is found somewhere

				return fmt.Errorf("failed to open chunk '%x' at '%s': %v", k, p, err)
			}

			//@TODO decrypt chunk
			//@TODO verify chunk content

			//copy chunk bytes to output
			defer f.Close()
			n, err := io.Copy(w, f)
			if err != nil {
				return fmt.Errorf("failed to copy chunk '%x' content after %d bytes: %v", k, n, err)
			}

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

package bits

import (
	"bufio"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/VividCortex/ewma"
	"github.com/boltdb/bolt"
	"github.com/dustin/go-humanize"
	"github.com/restic/chunker"
)

//RemoteChunk indicates a certain chunk is know but stored remotely
var RemoteChunk = []byte{}

var (
	ErrAlreadyPushed = fmt.Errorf("chunk is already pushed to the remote")
)

var (
	//ChunkBufferSize determines the size of the buffer that wil hold each chunk
	ChunkBufferSize = 8 * 1024 * 1024 //8MiB

	//RemoteBranchSuffix identifies the specialty branches used for persisting remote information
	RemoteBranchSuffix = "bits-remote"
)

var (
	//IndexBucket holds remotely whether chunks are stored remotely
	IndexBucket = []byte("index")
)

//Repository provides an abstraction on top of a Git repository for a
//certain directory that is queried by git commands
type Repository struct {
	//Path the to the Git executable we're using
	exe string

	//Path to the Git database directory (.git)
	gitDir string

	//Path to the local chunk storage
	chunkDir string

	//Path to the root of the root of the git projet
	rootDir string

	//stderr from executions will be written here
	output io.Writer

	//Header key allows us to recognize the start of a key listing
	header []byte

	//Footer Key allows us to recognize the end of a key listing
	footer []byte

	//remotes hold the remote chunk store we're using
	remote Remote

	//bits specific configuration
	conf *Conf

	//this channel receives any chunk Key that is hanled in an any operation
	keyProgressCh chan KeyOp

	//is called when a chunk was handled in any operation, can be called
	//concurrently
	KeyProgressFn func(KeyOp, float64)
}

//NewRepository sets up an interface on top of a Git repository in the
//provided directory. It will fail if the get executable is not in
//the shells PATH or if the directory doesnt seem to be a Git repository
func NewRepository(dir string, output io.Writer) (repo *Repository, err error) {
	repo = &Repository{}
	repo.exe, err = exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("git executable couldn't be found in your PATH: %v, make sure git it installed", err)
	}

	//ask git for the root directory
	repo.rootDir = dir
	buf := bytes.NewBuffer(nil)
	err = repo.Git(nil, nil, buf, "rev-parse", "--show-toplevel")
	repo.rootDir = strings.TrimSpace(buf.String())
	if err != nil || repo.rootDir == "" {
		return nil, fmt.Errorf("couldn't get git repo root, are you in a git repository?")
	}

	//we store the git directory seperately
	buf = bytes.NewBuffer(nil)
	err = repo.Git(nil, nil, buf, "rev-parse", "--git-dir")
	repo.gitDir = filepath.Join(repo.rootDir, strings.TrimSpace(buf.String()))
	if err != nil {
		return nil, fmt.Errorf("couldn't get git directory, are you in a git repository?")
	}

	//make sure command output is visible
	repo.output = output
	if repo.output == nil {
		repo.output = os.Stderr
	}

	//for now, store chunks in the .git directory
	repo.chunkDir = filepath.Join(repo.gitDir, "chunks")
	err = os.MkdirAll(repo.chunkDir, 0777)
	if err != nil {
		return nil, fmt.Errorf("couldnt setup chunk directory at '%s': %v", repo.chunkDir, err)
	}

	//setup header and footers
	repo.header = []byte("--- to use this file decode it with the 'git-bits' extension ---\n")
	repo.footer = []byte("----------------------- end of chunks --------------------------\n")
	if len(repo.header) != (hex.EncodedLen(KeySize)+1) || len(repo.footer) != (hex.EncodedLen(KeySize)+1) {
		return nil, fmt.Errorf("repository header and footer size are not '%d': header: %d, footer: %d", hex.EncodedLen(KeySize)+1, len(repo.header), len(repo.footer))
	}

	//setup configuration
	repo.conf = DefaultConf()
	err = repo.conf.OverwriteFromGit(repo)
	if err != nil {
		return nil, fmt.Errorf("failed to load bits configuration from git: %v", err)
	}

	//if a bucket is configured we will attempt to configured
	if repo.conf.AWSS3BucketName != "" {
		repo.remote, err = NewS3Remote(
			repo,
			"origin",
			repo.conf.AWSS3BucketName,
			repo.conf.AWSAccessKeyID,
			repo.conf.AWSSecretAccessKey,
			repo.conf.AWSDomain,
		)

		if err != nil {
			return nil, fmt.Errorf("unable to setup chunk remote: %v", err)
		}
	}

	//default output function will do basic logging of key progress
	indexBucketMax := 500
	indexedTotalKeys := 0
	repo.KeyProgressFn = func(kop KeyOp, tp float64) {
		if kop.Op == IndexOp {
			indexedTotalKeys++
			if indexedTotalKeys%indexBucketMax == 0 {
				fmt.Fprintf(repo.output, "indexed %d remote chunks, total: ~%s\n", indexBucketMax, humanize.FormatInteger("#.", indexedTotalKeys))
			}

			return
		}

		if kop.Op != IndexOp && indexedTotalKeys > 0 {
			fmt.Fprintf(repo.output, "indexing of remote chunks ended, total: ~%s\n", humanize.FormatInteger("#.", indexedTotalKeys))
			indexedTotalKeys = 0
		}

		if kop.Skipped {
			fmt.Fprintf(repo.output, "%x (skip: already %s)\n", kop.K, strings.Replace(fmt.Sprintf("%sed", string(kop.Op)), "ee", "e", 1))
		} else {
			fmt.Fprintf(repo.output, "%x (%s) %s/s\n", kop.K, string(kop.Op), humanize.Bytes(uint64(tp)))
		}
	}

	//we start handling key events while keeping a moving
	//average for the number of bytes moving through
	repo.keyProgressCh = make(chan KeyOp, 1)
	go func() {
		lastT := time.Now()
		e := ewma.NewMovingAverage()
		for kop := range repo.keyProgressCh {
			nowT := time.Now()
			diffD := nowT.Sub(lastT)
			if kop.CopyN > 0 {
				tp := float64(kop.CopyN) / diffD.Seconds()
				e.Add(tp)
			}

			repo.KeyProgressFn(kop, e.Value())
			lastT = nowT
		}
	}()

	return repo, nil
}

//Git runs the git executable with the working directory set to the repository director
func (repo *Repository) Git(ctx context.Context, in io.Reader, out io.Writer, args ...string) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}

	cmd := exec.CommandContext(ctx, repo.exe, args...)
	cmd.Dir = repo.rootDir
	cmd.Stderr = repo.output
	cmd.Stdin = in
	cmd.Stdout = out

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to run `git %v`: %v", strings.Join(args, " "), err)
	}

	return nil
}

//Install will prepare a git repository for usage with git bits, it configures
//filters, installs hooks and pulls chunks to write files in the current
//working tree. A configuration struct can be provided to populate local
//git configuration got future bits commands
func (repo *Repository) Install(w io.Writer, conf *Conf) (err error) {
	ctx := context.Background()

	//configure filter
	gconf := map[string]string{
		"filter.bits.clean":    "git bits split",
		"filter.bits.smudge":   "git bits fetch | git bits combine",
		"filter.bits.required": "true",
	}

	//add bits configuration
	if conf != nil {
		if conf.AWSS3BucketName != "" {
			gconf["bits.aws-s3-bucket-name"] = conf.AWSS3BucketName
		}

		if conf.AWSDomain != "" {
			gconf["bits.aws-s3-bucket-domain"] = conf.AWSDomain
		}

		if conf.AWSAccessKeyID != "" {
			gconf["bits.aws-access-key-id"] = conf.AWSAccessKeyID
		}

		if conf.AWSSecretAccessKey != "" {
			gconf["bits.aws-secret-access-key"] = conf.AWSSecretAccessKey
		}

		if conf.DeduplicationScope != 0 {
			gconf["bits.deduplication-scope"] = strconv.FormatUint(conf.DeduplicationScope, 10)
		}

		repo.conf = conf

		//@TODO init can complete remote configuration
		//@TODO obvious code duplication with constructor
		repo.remote, err = NewS3Remote(
			repo,
			"origin",
			repo.conf.AWSS3BucketName,
			repo.conf.AWSAccessKeyID,
			repo.conf.AWSSecretAccessKey,
			repo.conf.AWSDomain,
		)

		if err != nil {
			return fmt.Errorf("unable to setup default chunk remote: %v", err)
		}
	}

	//write configuration
	for k, val := range gconf {
		err := repo.Git(ctx, nil, nil, "config", "--local", k, val)
		if err != nil {
			return fmt.Errorf("failed to configure filter: %v", err)
		}
	}

	//write hook if doesnt exist yet
	hookp := filepath.Join(repo.gitDir, "hooks", "pre-push")
	f, err := os.OpenFile(hookp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0777)
	if err != nil {
		if os.IsExist(err) {
			fmt.Fprintf(repo.output, "a file already exists at '%s' already, skip writing git-bits hook\n", hookp)
		} else {
			return fmt.Errorf("couldnt setup hook: %v", err)
		}
	} else {
		defer f.Close()
		_, err = f.WriteString(`#!/bin/sh
			command -v git-bits >/dev/null 2>&1 || { echo >&2 "This project was setup with git-bits but it can (no longer) be found in your PATH: $PATH."; exit 0; }
			git-bits scan | git-bits push
	`)

		if err != nil {
			return fmt.Errorf("failed to git hook: %v", err)
		}
	}

	err = repo.Pull("HEAD", w)
	if err != nil {
		return fmt.Errorf("failed to pull chunks for HEAD: %v", err)
	}

	return nil
}

//ForEach is a convenient method for running logic for each chunk
//key in stream 'r', it will skip the chunk header and footer
func (repo *Repository) ForEach(r io.Reader, fn func(K) error) error {
	s := bufio.NewScanner(r)
	for s.Scan() {

		//and in any case skip it
		if bytes.Equal(s.Bytes(), repo.header[:len(repo.header)-1]) ||
			bytes.Equal(s.Bytes(), repo.footer[:len(repo.footer)-1]) {
			continue
		}

		//decode the actual keys
		data := make([]byte, hex.DecodedLen(len(s.Bytes())))
		_, err := hex.Decode(data, s.Bytes())
		if err != nil {
			return fmt.Errorf("failed to decode '%x' as hex: %v", s.Bytes(), err)
		}

		//check key length
		k := K{}
		if len(k) != len(data) {
			return fmt.Errorf("decoded chunk key '%x' has an invalid length %d, expected %d", data, len(data), len(k))
		}

		//fill K and hand it over
		copy(k[:], data[:KeySize])
		err = fn(k)
		if err != nil {
			return fmt.Errorf("failed to handle key '%x': %v", k, err)
		}
	}

	if err := s.Err(); err != nil {
		return fmt.Errorf("failed to scan chunk keys: %v", err)
	}

	return nil
}

//Push takes a list of chunk keys on reader 'r' and moves each chunk from
//the local storage to the remote store with name 'remote'. Prior to pushing
//the local index of the remote is updated so chunks are not uploaded twice.
func (repo *Repository) Push(store *bolt.DB, r io.Reader, remoteName string) (err error) {
	if repo.remote == nil {
		return fmt.Errorf("unable to push, no remote configured")
	}

	//err handling
	errs := []string{}
	errCh := make(chan error)
	defer close(errCh)
	go func() {
		for err := range errCh {
			errs = append(errs, fmt.Sprintf("%v", err))
		}
	}()

	//ask the remote to fetch all chunk keys
	pr, pw := io.Pipe()
	go func() {
		err = repo.remote.ListChunks(pw)
		defer pw.Close()
		if err != nil {
			errCh <- fmt.Errorf("failed to list remote chunk keys: %v", err)
		}
	}()

	//stream remote keys 500 at a time and write to local index concurrently
	//allowing some to be oppertunisticly combined to increase performance
	var wg sync.WaitGroup
	repo.ForEach(pr, func(k K) error {
		go func() {
			err = store.Batch(func(tx *bolt.Tx) error {
				wg.Add(1)
				defer wg.Done()
				b := tx.Bucket(IndexBucket)
				err = b.Put(k[:], RemoteChunk)
				if err != nil {
					return fmt.Errorf("failed to put '%x': %v", k, err)
				}

				repo.keyProgressCh <- KeyOp{IndexOp, k, false, 0}
				return nil
			})

			if err != nil {
				errCh <- fmt.Errorf("failed to batch indexed remote keys: %v", err)
			}
		}()

		return nil
	})

	//wait for all concurrent batch transactions to complete
	wg.Wait()
	if len(errs) > 0 {
		return fmt.Errorf("there were errors while indexing: \n %s", strings.Join(errs, "\n\t"))
	}

	//scan for chunk keys
	err = repo.ForEach(r, func(k K) (ferr error) {
		err = store.View(func(tx *bolt.Tx) error {
			b := tx.Bucket(IndexBucket)
			c := b.Get(k[:])
			if c == nil {
				return nil //doesnt exist
			}

			if bytes.Equal(c, RemoteChunk) {
				return ErrAlreadyPushed
			}

			return nil
		})

		//already pushed err is a good think, we can skip uploading this chunk!
		if err == ErrAlreadyPushed {
			repo.keyProgressCh <- KeyOp{PushOp, k, true, 0}
			return nil
		}

		if err != nil {
			return fmt.Errorf("failed to read index: %v", err)
		}

		//open local chunk file
		p, _ := repo.Path(k, false)
		f, err := os.OpenFile(p, os.O_RDONLY, 0666)
		if err != nil {
			return fmt.Errorf("failed to open chunk '%x' at '%s' for pushing: %v", k, p, err)
		}

		//get remote writer
		defer f.Close()
		wc, err := repo.remote.ChunkWriter(k)
		if err != nil {
			return fmt.Errorf("failed to get chunk writer: %v", err)
		}

		//start upload
		defer wc.Close()
		n, err := io.Copy(wc, f)
		if err != nil {
			return fmt.Errorf("failed to copy file '%s' to remote writer after %d bytes: %v", f.Name(), n, err)
		}

		//indicate we pushed the chunk
		repo.keyProgressCh <- KeyOp{PushOp, k, false, n}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to loop over each key: %v", err)
	}

	return nil
}

//Fetch takes a list of chunk keys on reader 'r' and will try to fetch chunks
//that are not yet stored locally. Chunks that are already stored locally should
//result in a no-op, all keys (fetched or not) will be written to 'w'.
func (repo *Repository) Fetch(r io.Reader, w io.Writer) (err error) {
	printk := func(k K) error {
		_, err := fmt.Fprintf(w, "%x\n", k)
		return err
	}

	return repo.ForEach(r, func(k K) error {

		//setup chunk path
		p, err := repo.Path(k, true)
		if err != nil {
			return fmt.Errorf("failed to create chunk path for key '%x': %v", k, err)
		}

		//attempt to open, if its already assume it was written concurrently
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0666)
		if err != nil {
			if os.IsExist(err) {
				repo.keyProgressCh <- KeyOp{FetchOp, k, true, 0}
				return printk(k)
			}

			return fmt.Errorf("failed to open chunk file '%s' for writing: %v", p, err)
		}

		if repo.remote == nil {
			return fmt.Errorf("key '%x' isn't stored locally, but no remote is configured", k)
		}

		rc, err := repo.remote.ChunkReader(k)
		if err != nil {
			return fmt.Errorf("failed to get chunk reader for key '%x': %v", k, err)
		}

		defer rc.Close()
		n, err := io.Copy(f, rc)
		if err != nil {
			return fmt.Errorf("failed to clone chunk '%x' from remote: %v", err)
		}

		//indicate we fetched a key
		repo.keyProgressCh <- KeyOp{FetchOp, k, false, n}
		return printk(k)
	})
}

//Path returns the local path to the chunk file based on the key, it can
//create required directories when 'mkdir' is set to true, in that case
//err might container directory creation failure.
func (repo *Repository) Path(k K, mkdir bool) (p string, err error) {
	dir := filepath.Join(repo.chunkDir, fmt.Sprintf("%x", k[:2]))
	if mkdir {
		err = os.MkdirAll(dir, 0777)
		if err != nil {
			return "", fmt.Errorf("failed to create chunk dir '%s': %v", dir, err)
		}
	}

	return filepath.Join(dir, fmt.Sprintf("%x", k[2:])), nil
}

//LocalStore will return the local chunk store, creating it in the
//repositories chunk directory if it doesnt exist yet. It creates
//the necessary buckets if they dont exist yet
func (repo *Repository) LocalStore() (db *bolt.DB, err error) {
	dbpath := filepath.Join(repo.chunkDir, "a.chunks")
	db, err = bolt.Open(dbpath, 0666, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("failed to open chunks database '%s': %v", dbpath, err)
	}

	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(IndexBucket)
		if err != nil {
			return fmt.Errorf("failed to create bucket: %s", err)
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create bucket '%s': %v", string(IndexBucket), err)
	}

	return db, nil
}

//Pull get all file paths of blobs that hold chunk keys in the provided ref
//and combine the chunks in them into their original file, fetching any chunks
//not currently available in the local store
func (repo *Repository) Pull(ref string, w io.Writer) (err error) {

	// ls-tree -r -l | f1 | f2 | git update-index -q --refresh --stdin
	ctx := context.Background()
	r1, w1 := io.Pipe()
	r2, w2 := io.Pipe()
	r3, w3 := io.Pipe()

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
		err = repo.Git(ctx, nil, w1, "ls-tree", "-r", "-l", ref)
		if err != nil {
			//@TODO this will error if the repository is empty (no commits yet)
			//probaly throw a warning instead
			// errCh <- err
		}
	}()

	go func() {
		defer w2.Close()
		s := bufio.NewScanner(r1)
		for s.Scan() {

			//@see https://git-scm.com/docs/git-ls-tree
			//line : <mode> SP <type> SP <object> TAB <file>, we use the
			//tab to be able to clearly seperate the file name as it may contain
			//field seperating characters
			tfields := bytes.SplitN(s.Bytes(), []byte("\t"), 2)
			fields := bytes.Fields(s.Bytes())
			if len(fields) < 5 || len(tfields) != 2 || !bytes.Equal(fields[1], []byte("blob")) {
				continue
			}

			objSize, err := strconv.ParseInt(string(fields[3]), 10, 64)
			if err != nil {
				errCh <- err
				continue
			}

			//all key files have a size that is the exact multiple of
			//33 bytes: 32 bytes hex encoded hashes with a newline character
			if objSize%int64(hex.EncodedLen(KeySize)+1) != 0 {
				continue
			}

			fmt.Fprintf(w2, "%s\n", tfields[1])
		}

		if err = s.Err(); err != nil {
			errCh <- err
		}
	}()

	go func() {
		defer w3.Close()
		s := bufio.NewScanner(r2)
		for s.Scan() {
			err = func() error {
				fpath := filepath.Join(repo.rootDir, s.Text())
				tmpfpath := ""

				err = func() error {
					f, err := os.OpenFile(fpath, os.O_RDWR|os.O_CREATE, 0666)
					if err != nil {
						return err
					}

					defer f.Close()
					hdr := make([]byte, hex.EncodedLen(KeySize))
					_, err = f.Read(hdr)
					if err != nil {
						//if we cant even read a complete header, its not gonna contain chunks
						return nil
					}

					offs, err := f.Seek(0, 0)
					if err != nil || offs != 0 {
						return fmt.Errorf("failed to seek files: %v", err)
					}

					if !bytes.Equal(hdr, repo.header[:len(repo.header)-1]) {
						return nil
					}

					//We know its a chunks file that needs filling
					tmpf, err := ioutil.TempFile("", "bits_tmp_")
					if err != nil {
						return err
					}

					tmpfpath = tmpf.Name()
					defer tmpf.Close()
					fi, err := f.Stat()
					if err != nil {
						return fmt.Errorf("failed to stat original file for permissions: %v", err)
					}

					//mod the tempfile as the original
					err = os.Chmod(tmpfpath, fi.Mode())
					if err != nil {
						return fmt.Errorf("failed to modify temp file permissions: %v", err)
					}

					pr, pw := io.Pipe()
					go func() {
						defer pw.Close()
						err = repo.Fetch(f, pw)
						if err != nil {
							errCh <- err
						}
					}()

					err = repo.Combine(pr, tmpf)
					if err != nil {
						return fmt.Errorf("failed to combine: %v", err)
					}

					return nil
				}()

				if err != nil {
					return err
				}

				//no tmpfpath means we have no files to move, wer're done here
				if tmpfpath == "" {
					return nil
				}

				err = os.Remove(fpath)
				if err != nil {
					return fmt.Errorf("failed to remove original file '%s': %v", fpath, err)
				}

				err = os.Rename(tmpfpath, fpath)
				if err != nil {
					return fmt.Errorf("failed to move '%s' to '%s'", tmpfpath, s.Text())
				}

				fmt.Fprintf(w3, "%s\n", fpath)
				return nil
			}()

			if err != nil {
				errCh <- fmt.Errorf("failed to check file '%s' for header content: %v", err)
			}
		}
	}()

	err = repo.Git(ctx, r3, nil, "update-index", "-q", "--refresh", "--stdin")
	if err != nil {
		return fmt.Errorf("failed to update index: %v", err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("there were scanning errors: \n %s", strings.Join(errs, "\n\t"))
	}

	return nil
}

func (repo *Repository) ScanEach(r io.Reader, w io.Writer) (err error) {
	s := bufio.NewScanner(r)
	for s.Scan() {
		fields := bytes.Fields(s.Bytes())
		left := ""
		right := ""

		switch len(fields) {
		case 4: //push hook format
			right = string(fields[1])
			left = string(fields[3])
			if left == "0000000000000000000000000000000000000000" {
				left = ""
			}
		case 1: //scan refs (left empty)
			right = string(fields[0])
		case 2: //scan refs
			right = string(fields[0])
			left = string(fields[1])
		default: //error
			return fmt.Errorf("unexpected input for scanning: %s", s.Text())
		}

		return repo.Scan(left, right, w)
	}

	return s.Err()
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
		args := []string{"rev-list", "--objects", right}
		if left != "" {
			args = append(args, "^"+left)
		}

		err = repo.Git(ctx, nil, w1, args...)
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
//space, pushing these to a remote store happens at a later time (pre-push hook)
func (repo *Repository) Split(r io.Reader, w io.Writer) (err error) {
	if repo.conf.DeduplicationScope == 0 {
		return fmt.Errorf("no deduplication scope configured, please run init", err)
	}

	//create a buffer that allows us to peek if this is a file that
	//is already spit, if so: simply copy over the bytes, nothing to split
	bufr := bufio.NewReader(r)
	hdr, _ := bufr.Peek(hex.EncodedLen(KeySize) + 1)
	if bytes.Equal(hdr, repo.header) {
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
	chunkr := chunker.New(bufr, chunker.Pol(repo.conf.DeduplicationScope))
	buf := make([]byte, ChunkBufferSize)
	for {
		chunk, err := chunkr.Next(buf)
		if err == io.EOF {
			break
		}

		if err != nil {
			return fmt.Errorf("Failed to write chunk (%d bytes) to buffer (size %d bytes): %v", chunk.Length, ChunkBufferSize, err)
		}

		//@TODO use hmac(SHA256) with the deduplication scope as a key
		k := sha256.Sum256(chunk.Data)
		printk := func(k K) error {
			_, err = fmt.Fprintf(w, "%x\n", k)
			if err != nil {
				return fmt.Errorf("failed to write key to output: %v", err)
			}

			return nil
		}

		err = func() error {

			//formulate path
			p, err := repo.Path(k, true)
			if err != nil {
				return fmt.Errorf("failed to create chunk dir for '%x': %v", k, err)
			}

			//attempt to open, create if nont existing
			f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0666)
			if err != nil {

				//if its already written, all good; output key
				if os.IsExist(err) {
					repo.keyProgressCh <- KeyOp{StageOp, k, true, 0}
					return printk(k)
				}

				return fmt.Errorf("Failed to open chunk file '%s' for writing: %v", p, err)
			}

			//aes encryption with
			block, err := aes.NewCipher(k[:])
			if err != nil {
				return fmt.Errorf("failed to create cipher for key '%x': %v", k, err)
			}

			//create encrypt writer
			//@TODO use GCM cipher mode
			//@TODO	If the key is unique for each ciphertext, then it's ok to use a zero IV.
			defer f.Close()
			var iv [aes.BlockSize]byte
			stream := cipher.NewOFB(block, iv[:])
			encryptw := &cipher.StreamWriter{S: stream, W: f}

			//encrypt and write to file
			n, err := encryptw.Write(chunk.Data)
			if err != nil {
				return fmt.Errorf("Failed to write chunk '%x' (wrote %d bytes): %v", k, n, err)
			}

			//report staging and output key
			repo.keyProgressCh <- KeyOp{StageOp, k, false, int64(n)}
			return printk(k)
		}()

		if err != nil {
			return fmt.Errorf("Failed to split chunk '%x': %v", k, err)
		}
	}

	return nil
}

//Combine turns a newline seperated list of chunk keys from 'r' by reading the the
//projects local store. Chunks are then decrypted and combined in the original
//file and written to writer 'w'
func (repo *Repository) Combine(r io.Reader, w io.Writer) (err error) {
	err = repo.ForEach(r, func(k K) error {

		//open chunk file
		p, _ := repo.Path(k, false)
		f, err := os.OpenFile(p, os.O_RDONLY, 0666)
		if err != nil {
			return fmt.Errorf("failed to open chunk '%x' locally at '%s': %v", k, p, err)
		}

		//setup aes cipher
		block, err := aes.NewCipher(k[:])
		if err != nil {
			return fmt.Errorf("failed to create cipher: %v", err)
		}

		//setup the read stream
		//@TODO use GCM cipher mode
		//@TODO	If the key is unique for each ciphertext, then it's ok to use a zero IV.
		var iv [aes.BlockSize]byte
		stream := cipher.NewOFB(block, iv[:])
		decryptr := &cipher.StreamReader{S: stream, R: f}

		//copy chunk bytes to output
		defer f.Close()
		n, err := io.Copy(w, decryptr)
		if err != nil {
			return fmt.Errorf("failed to copy chunk '%x' content after %d bytes: %v", k, n, err)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to loop over keys: %v", err)
	}

	return nil
}

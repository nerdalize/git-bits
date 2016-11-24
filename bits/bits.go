package bits

import (
	"io"
)

//KeySize describes the size of each chunk ley
const KeySize = 32

//Chunks holds opaque binary data
type Chunk []byte

//Op describes possible key operations
type Op string

//KeyOp descibes a key operation
type KeyOp struct {
	Op      Op
	K       K
	Skipped bool
}

var (
	//PushOp tells a chunk was/is pushed to a remote
	PushOp = Op("push")

	//FetchOp tells a chunk was/is fetched from a remote
	FetchOp = Op("fetch")

	//StageOd tells a chunk is staged locally
	StagedOp = Op("stage")
)

//K are 32-byte chunk keys, de-duplicated lookups and
//convergent encryption setup assume this this to be
//a (cryptographic) hash of plain-text chunk content
type K [KeySize]byte

//Remote describes a method for streaming chunk information
type Remote interface {
	ChunkReader(k K) (rc io.ReadCloser, err error)
	ChunkWriter(k K) (wc io.WriteCloser, err error)
	ListChunks(w io.Writer) (err error)
}

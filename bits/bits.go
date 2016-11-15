package bits

import (
	"io"
)

// COMMAND INTERFACE
//
// [Clean] - goals it turn a plain file into encrypted, deduplicated and persisted chunks
// and output keys for those chunks. Chunks are written to a local chunk space, pushing these
// to a remote store happens at a later time (pre-push hook) but a log of key file blob hashes
// is kept to recognize them during a push.
//
//                                         														 /-> FSIndex<Index>.Add(k) 	--------------------------\
//                 /--> Hash.Hash(chunk) --> Crypto.Encrypt(k, chunk) -|                                               ... ------> KeyFileLog.Add() -> KeyEncoding.Encode(k, STDOUT)
// CBC.Split(STDIN)---> ...          																	 \-> FSObjectStore<ObjectStore>.WriteChunk(k, chunk)
//                 \--> ...

// [Smudge] - goal is to get chunks by their key from either the local store or - if it cannot be found
// locally - attempt to fetch it from a remote and place it locally, then decrypt and combine the chunk into
// the original file.
//
//                           /-> ...       																																																																																								...  ---\
// KeyEncoding.Decode(STDIN) --> ... 																																																								 	 /-> FSIndexStore<Index>.Add(k)																		 --> CBC.Combine(STDOUT)
//                           \-> FSIndexStore<Index>.HasKey(k)                                            																						/-> FSObjectStore<ObjectStore>.WriteChunk(chunk)      											/
//                             -> GitIndexStore<Index>.HasKey(k) -> GitIndexStore<SharedIndexStore>.Pull()->S3ObjectStore<ObjectStore>.ReadChunk(k) -> Crypto.Decrypt(k, chunk) ----------------------> Hash.Verify(k, chunk) ---

// [Push] - pre-push hook has three goals 1) pushing chunks from a local chunk store to a remote one
// based on the files commits that are actully pushed 2) updating the GitIndexStore with pushed keys
// 3) push the index branch itself to the git remote so it can be shared
//
//------------------ [ GIT REF LOOKUP ] ----------------+
//                                                      |                              /--> ...
// [readCommits -> c1,c2,c3 -> readBlobs -> b1, b2, b3] | ---> KeyEncoding.Decode(b1) ---> FSObjectStore<ObjectStore>.ReadChunk(k) -> S3ObjectStore<ObjectStore>.WriteChunk(k) -> GitIndexStore<IndexStore>.WriteKey(k)
//																											|  \--> ...                    \--> ...
// -----------------------------------------------------+
//
// GitIndexStore<SharedIndexStore>.Push()

//Chunks holds opaque binary data
type Chunk []byte

//K are 32-byte chunk keys, de-duplicated lookups and
//convergent encryption setup assume this this to be
//a (cryptographic) hash of plain-text chunk content
type K [32]byte

//CBC provides content-based chunking of data
type CBC interface {
	Split(r io.Reader) (chunks []Chunk, err error)
	Combine(chunks map[K]Chunk, w io.Writer) (err error)
}

//Hash provides chunk hashing and verification
type Hash interface {
	Hash(chunk Chunk) (key K, err error)
	Verify(chunk Chunk, k K) (err error)
}

//Crypto provides encryption and decryption of chunks
type Crypto interface {
	Encrypt(key K, plain Chunk) (encrypted Chunk, err error)
	Decrypt(key K, encrypted Chunk) (plain Chunk, err error)
}

//Index provides an index of chunk keys
// type Index interface {
// 	Has(k K) (b bool, err error)
// 	Add(k K) (err error)
// 	Clear() (err error)
// }
//
// //SerializedIndex is an index that can be serialized into a byte stream
// type SerializedIndex interface {
// 	Serialize(w io.Writer) (err error)
// 	Deserialize(r io.Reader) (err error)
// }
//
// //StoredIndex is an index that can persisted
// type StoredIndex interface {
// 	Index
// 	SerializedIndex
// 	Save(ctx context.Context) (err error)
// 	Load(ctx context.Context) (err error)
// }
//
// //SharedIndex can be pushed and pulled from a shared location
// type SharedIndex interface {
// 	StoredIndex
// 	Pull(ctx context.Context) (err error)
// 	Push(ctx context.Context) (err error)
// }

//ObjectStore provides persistent storage for the actual chunks
type ObjectStore interface {
	WriteChunk(key K, chunk Chunk)
	ReadChunk(key K) (chunk Chunk, err error)
}

//KeyEncoding can encode and decode chunk keys from byte streams, such
//as os.File, and os.Stdin/os.Stdout etc.
type KeyEncoding interface {
	Decode(r io.Reader) (keys []K, err error)
	Encode(key []K, w io.Writer) (err error)
}

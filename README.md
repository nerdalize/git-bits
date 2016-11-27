# git-bits
[Git](https://git-scm.com/) is an awesome tool for versioning and storing your textual files but it can fall short when you're looking to use it to store large (binary) files. *git-bits* is an extension that builds on top of Git that solves this problem in a simple and secure fashion through clever usage of Content Based Chunking [(CBC)](https://en.wikipedia.org/wiki/Rolling_hash), Content-Addressable Storage [(CAS)](https://en.wikipedia.org/wiki/Content-addressable_storage) and [convergent encryption](https://en.wikipedia.org/wiki/Convergent_encryption). It written in pure and portable Go code and is structured as a set of streaming commands that follow the [unix philosopy](https://en.wikipedia.org/wiki/Unix_philosophy).

**Features:**

 - **Normal Git workflow**: it uses Git's *smudge/clean* filters with a *pre-push* hook to integrate seamlessly on top of your new or existing repository so you can continue to use your normal workflow. 
 - **No Server Process**: upon pushing your Git commits to a remote your large files are also send to a remote object store. By using a content-addressable storage scheme it doesn't require a coordinating server process that can become unavailable, it uploads directly to your own high-available [AWS S3](https://aws.amazon.com/s3/) bucket. 
 - **Deduplication**: Large files are stored in variable sized blocks based on the file's content. Each block is only stored once and as such it becomes economic to store many slightly-different versions. This allows for massive savings on both bandwidth and storage costs when you're large files only change partially between versions.
 - **Encryption-at-rest**: Since large files are now stored at a thrird party, seperate from your actual Git repository, it becomes important that the data is encrypted at rest. As such, `git-bits` encrypts each chunk using the [AES-256](https://en.wikipedia.org/wiki/Advanced_Encryption_Standard) encryption standard


## Installation

## Getting Started

## Encryption Disclaimers

## Git-Bits vs other software 
- git lfs 
- git annex 
- bup 
- gits3

## Roadmap
- end-to-end encryption for large file chunks
- more remote types
- flexible indexing
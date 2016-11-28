# git-bits
[Git](https://git-scm.com/) is an awesome tool for versioning and storing your textual files but it can fall short when you're looking to use it to store large (binary) files. *git-bits* is an extension that builds on top of Git that solves this problem in a simple and secure fashion through clever usage of Content Based Chunking [(CBC)](https://en.wikipedia.org/wiki/Rolling_hash), Content-Addressable Storage [(CAS)](https://en.wikipedia.org/wiki/Content-addressable_storage) and [convergent encryption](https://en.wikipedia.org/wiki/Convergent_encryption). It written in pure and portable Go code and is structured as a set of streaming commands that follow the [unix philosopy](https://en.wikipedia.org/wiki/Unix_philosophy).

**Features:**

 - **Normal Git workflow**: it uses Git's *smudge/clean* filters with a *pre-push* hook to integrate seamlessly on top of your new or existing repository so you can continue to use your normal workflow. 
 - **No Server Process**: upon pushing your Git commits to a remote your large files are also send to a remote object store. By using a content-addressable storage scheme it doesn't require a coordinating server process that can become unavailable, it uploads directly to your own high-available [AWS S3](https://aws.amazon.com/s3/) bucket. 
 - **Deduplication**: Large files are stored in variable sized blocks based on the file's content. Each block is only stored once and as such it becomes economic to store many slightly-different versions. This allows for massive savings on both bandwidth and storage costs when you're large files only change partially between versions.
 - **Encryption-at-rest**: Since large files are now stored at a third party, seperate from your actual Git repository, it becomes important that the data is encrypted at rest. `git-bits` encrypts each chunk using the [AES-256](https://en.wikipedia.org/wiki/Advanced_Encryption_Standard) encryption standard before uploading them.


## Installation
1. First, make sure [Git itself is installed](https://git-scm.com/downloads) and available in your `PATH`

2. Then, Choose one of you preferred installation methods for installing the _git-bits_ extension:
	
	__Pre-compiled binaries__ are available for 64bit __Windows__,__MacOS__ and __Linux__ on the [release page](https://github.com/nerdalize/git-bits/releases), simply download the binary for your platform and place it in your `PATH`. 

	__building from source__ is recommended for other platforms, this is made easy by the fact that `git-bits` is go-gettable. Simply install the [Go SDK](https://golang.org/doc/install), make sure your `$GOPATH` is setup and `$GOPATH/bin` is added to you `PATH`. Then run the following to install or update:

	```
	go get -u github.com/nerdalize/git-bits
	``` 

3. Verify that the installation succeeded by envoking the Git with the *git-bits* extension, it should show the _git-bits_ subcommands. If it complains with "... is not a git command", make sure the above steps were executed correctly.

	```
	git bits
	```


## Getting Started
_git-bits_ is build on top of Git, this guide assumes you have basic knowledge of working with a Git repository. Also, large file chunks are stored directly on AWS S3, as such you'll need a AWS account with an S3 bucket and a `access_key_id` and the `secret_access_key` to allow _git-bits_ to put, get and list bucket objects. The bucket needs to be completely reserved for _git-bits_ file chunks.

   *Note: For Windows, the documentation assumes you're using Git through a bash-like CLI but nothing about the implementation prevents you from using another approach.*

  1. Use your terminal to navigate to a repository with some large/binary files you would like to store and initialize _git-bits_:

  ```
  cd ~/my-project
  git bits install
  ```
  
  *NOTE: If your git repository doesn't have any commits, a seemingly 'fatal' error appears, you can safely ignore this*

  2. Provide your AWS information when asked and _git-bits_ will configure a pre-push hook and the correct Git filter. 

  3. The 'bits' filter requires you mark certain files for large-file storage using the `.gitattributes` file, the following marks all files ending with .bin for storage using _git-bits_: 

  ```
  echo '*.bin  filter=bits' >> .gitattributes
  ```

  4. With the filter inplace you can now add your large file to the staging area and commit changes as usual. Upon moving large-files to the staging area, _git-bits_  will split them into variable sized chunks and write them to `.git/chunks`, the key of each chunk will be listen to inform you of the progress: 

  ```
  git add ./my-large-file.bin
  git commit -m "added a large file"
  ```
  
  5. Finally, to store your large files on S3 you can simply push the changes as you're used to. _git-bits_ will index what chunks are already present and only upload new blocks: 

  ```
  git push
  ```
  
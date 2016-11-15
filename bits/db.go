package bits

import (
	"fmt"
	"os"

	"github.com/boltdb/bolt"
)

var (
	//LogBucketName determines the name of the db bucket that holds clean logs
	LogBucketName = []byte("logs_v1")
)

type DB struct {
	db *bolt.DB
}

func NewDB(path string) (db *DB, err error) {
	db = &DB{}

	db.db, err = bolt.Open(path, 0777, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open db at '%s': %v", path, err)
	}

	return db, db.db.Update(func(tx *bolt.Tx) (err error) {
		_, err = tx.CreateBucketIfNotExists(LogBucketName)
		return err
	})
}

//Cleaned returns hashes of all blobs that were cleaned
func (db *DB) Cleaned() (sha1s [][]byte, err error) {
	err = db.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(LogBucketName).ForEach(func(k, v []byte) error {
			fmt.Fprintf(os.Stderr, "key=%x, value=%x\n", k, v)
			return nil
		})
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list clean logs: %v", err)
	}

	return sha1s, nil
}

//LogClean will record a cleaned file using the sha1
func (db *DB) LogClean(sha1 []byte) (err error) {
	err = db.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(LogBucketName).Put(sha1, []byte{})
	})

	if err != nil {
		return fmt.Errorf("failed to update: %v", err)
	}

	return nil
}

package main

import (
	"crypto/sha256"
	"hash"
)

// newHash is a tiny shim so the seed_all.go file doesn't need to grow its
// import block — kept in a separate file to keep seed_all readable.
func newHash() hash.Hash { return sha256.New() }

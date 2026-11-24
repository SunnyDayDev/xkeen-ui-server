// Package agentcredential owns the shared wire encoding of agent credentials.
package agentcredential

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

type Kind string

const (
	Invitation Kind = "i1"
	Working    Kind = "w1"
	Size            = 69
)

// Proof is the non-secret selector and the digest of the random secret.
type Proof struct {
	Selector [16]byte
	Digest   [sha256.Size]byte
}

type Token struct {
	kind     Kind
	selector [16]byte
	secret   [32]byte
}

var encoding = base64.RawURLEncoding.Strict()

func Generate(kind Kind) Token {
	if kind != Invitation && kind != Working {
		panic("invalid agent credential kind")
	}
	token := Token{kind: kind}
	rand.Read(token.selector[:])
	rand.Read(token.secret[:])
	return token
}

func Parse(encoded string, kind Kind) (Token, bool) {
	if (kind != Invitation && kind != Working) || len(encoded) != Size ||
		encoded[:2] != string(kind) || encoded[2] != '.' || encoded[25] != '.' {
		return Token{}, false
	}
	token := Token{kind: kind}
	n, err := encoding.Decode(token.selector[:], []byte(encoded[3:25]))
	if err != nil || n != len(token.selector) {
		return Token{}, false
	}
	n, err = encoding.Decode(token.secret[:], []byte(encoded[26:]))
	if err != nil || n != len(token.secret) || encoded != token.Encode() {
		return Token{}, false
	}
	return token, true
}

func (token Token) Encode() string {
	return string(token.kind) + "." + encoding.EncodeToString(token.selector[:]) + "." +
		encoding.EncodeToString(token.secret[:])
}

func (token Token) Proof() Proof {
	return Proof{Selector: token.selector, Digest: sha256.Sum256(token.secret[:])}
}

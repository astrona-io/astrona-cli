// Package transport holds this computer's device keys and what is signed
// with them.
//
// `astrona login` makes two key pairs: an Ed25519 signing key, which proves
// every call and every `astrona connect` comes from this computer, and an
// X25519 key that a later version uses to encrypt the socket (registered now
// so devices do not have to enrol twice). Only the public halves leave the
// computer. The private halves live in ~/.astrona/device-key.json (0600);
// they are never sent, logged or printed.
//
// Signed messages are built by Message: each part is a 4-byte big-endian
// length followed by its bytes, so no two different lists of parts produce
// the same message. astrona-svc-labs (app/transport) builds them the same
// way.
package transport

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
)

// Labels that start each signed message, one per purpose.
const (
	KeyLabel     = "astrona-device-key-v1"
	ConnectLabel = "astrona-connect/1"
	RequestLabel = "astrona-request/1"
)

// Key is this computer's two key pairs. ID is the fingerprint the site and
// `astrona whoami` show.
type Key struct {
	ID      string
	signing ed25519.PrivateKey
	dh      *ecdh.PrivateKey
}

// Generate makes a new pair of keys from the operating system's random
// source.
func Generate() (*Key, error) {
	_, signing, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	dh, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return newKey(signing, dh), nil
}

func newKey(signing ed25519.PrivateKey, dh *ecdh.PrivateKey) *Key {
	k := &Key{signing: signing, dh: dh}
	k.ID = Fingerprint(k.SigningPublic(), k.DHPublic())
	return k
}

// fromSeeds rebuilds a key from its saved private halves.
func fromSeeds(signingSeed, dhPrivate []byte) (*Key, error) {
	if len(signingSeed) != ed25519.SeedSize {
		return nil, errors.New("the signing key is not 32 bytes")
	}
	dh, err := ecdh.X25519().NewPrivateKey(dhPrivate)
	if err != nil {
		return nil, errors.New("the X25519 key is not valid")
	}
	return newKey(ed25519.NewKeyFromSeed(signingSeed), dh), nil
}

// SigningPublic is the raw 32-byte Ed25519 public key.
func (k *Key) SigningPublic() []byte {
	return []byte(k.signing.Public().(ed25519.PublicKey))
}

// DHPublic is the raw 32-byte X25519 public key.
func (k *Key) DHPublic() []byte { return k.dh.PublicKey().Bytes() }

func (k *Key) sign(parts ...[]byte) []byte {
	return ed25519.Sign(k.signing, Message(parts...))
}

// Registration is what a sign-in sends for this key: both public keys and
// key_proof, all base64url. bound, when not nil, is signed too: an existing
// sign-in that enrols its key binds the proof to its refresh token's
// SHA-256.
type Registration struct {
	SigningKey string `json:"signing_key"`
	DHKey      string `json:"dh_key"`
	KeyProof   string `json:"key_proof"`
}

// Register returns the key's registration, bound to bound when it is not nil.
func (k *Key) Register(bound []byte) Registration {
	parts := [][]byte{[]byte(KeyLabel), k.SigningPublic(), k.DHPublic()}
	if bound != nil {
		parts = append(parts, bound)
	}
	return Registration{
		SigningKey: encode(k.SigningPublic()),
		DHKey:      encode(k.DHPublic()),
		KeyProof:   encode(k.sign(parts...)),
	}
}

// Message joins parts as 4-byte big-endian length plus bytes each.
func Message(parts ...[]byte) []byte {
	n := 0
	for _, p := range parts {
		n += 4 + len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = binary.BigEndian.AppendUint32(out, uint32(len(p))) //nolint:gosec // parts are small: keys, hashes, a path
		out = append(out, p...)
	}
	return out
}

// Fingerprint is the first 16 bytes of SHA-256(signing ‖ dh), base32
// without padding, in groups of four: ABCD-EFGH-…-XY (32 characters).
func Fingerprint(signing, dh []byte) string {
	sum := sha256.Sum256(append(append([]byte{}, signing...), dh...))
	text := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:16])
	var groups []string
	for i := 0; i < len(text); i += 4 {
		groups = append(groups, text[i:min(i+4, len(text))])
	}
	return strings.Join(groups, "-")
}

// Hash is SHA-256, the form tokens and bodies are signed in.
func Hash(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

func encode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func decode(s string, size int) ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil || len(b) != size {
		return nil, errors.New("not base64url of the expected size")
	}
	return b, nil
}

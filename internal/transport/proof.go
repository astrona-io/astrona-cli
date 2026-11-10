package transport

import (
	"crypto/rand"
	"errors"
	"strconv"
	"strings"
)

// Header is the HTTP header that carries a call's proof.
const Header = "Astrona-Proof"

// nonceBytes is the size of a request proof's random nonce.
const nonceBytes = 16

// challengeBytes is the size of the nonce in the socket's challenge.
const challengeBytes = 32

// RequestProof is the Astrona-Proof header for one HTTPS call: an Ed25519
// signature over the method, the path on the site, the time (Unix seconds),
// a random nonce, and the SHA-256 of the bearer token and of the exact body
// sent (empty for a call without one). token is the access token, or the
// refresh token on the refresh grant.
func (k *Key) RequestProof(method, path string, unix int64, token string, body []byte) (string, error) {
	nonce := make([]byte, nonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ts := strconv.FormatInt(unix, 10)
	sig := k.sign(
		[]byte(RequestLabel),
		[]byte(strings.ToUpper(method)),
		[]byte(path),
		[]byte(ts),
		nonce,
		Hash([]byte(token)),
		Hash(body),
	)
	return "v=1; kid=" + k.ID + "; ts=" + ts + "; n=" + encode(nonce) + "; sig=" + encode(sig), nil
}

// ConnectProof answers the socket's challenge: an Ed25519 signature over
// the challenge's nonce (base64url, as sent), its connection id and the
// SHA-256 of the access token the socket was opened with. It is sent as
// the hello's "proof".
func (k *Key) ConnectProof(nonce, connectionID, accessToken string) (string, error) {
	raw, err := decode(nonce, challengeBytes)
	if err != nil {
		return "", errors.New("the site's challenge is malformed")
	}
	if connectionID == "" || len(connectionID) > 64 {
		return "", errors.New("the site's challenge has no connection id")
	}
	return encode(k.sign([]byte(ConnectLabel), raw, []byte(connectionID), Hash([]byte(accessToken)))), nil
}

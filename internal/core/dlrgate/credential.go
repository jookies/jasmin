package dlrgate

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
)

const (
	// TokenPrefix marks a registry token in a log, a config file or a support
	// ticket, so one can be recognised — and revoked — without guessing what it
	// belongs to. It follows the message-pull credential's convention.
	TokenPrefix = "syndlr_"
	// tokenEntropyBytes is the random part of a token.
	tokenEntropyBytes = 32
	// keyIDBytes is the random part of the public key id that appears in the
	// URL. It is not a secret, but it is unguessable so a partner's endpoint
	// cannot be found by enumeration, and it does not disclose the username to
	// whoever holds the URL.
	keyIDBytes = 16
)

// keyIDPattern bounds what the router will accept as a key id, so a malformed
// path segment is rejected before it reaches a digest lookup.
var keyIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

// Credential is a freshly minted per-user registry credential. Token is the
// plaintext, which exists only in the response that created it.
type Credential struct {
	KeyID       string
	Token       string
	TokenSHA256 string
}

// NewCredential mints a per-user registry credential: the public key id for the
// URL, the plaintext token to hand over once, and the hex digest to store.
func NewCredential() (Credential, error) {
	idRaw := make([]byte, keyIDBytes)
	if _, err := rand.Read(idRaw); err != nil {
		return Credential{}, fmt.Errorf("dlrgate: generate key id: %w", err)
	}
	tokenRaw := make([]byte, tokenEntropyBytes)
	if _, err := rand.Read(tokenRaw); err != nil {
		return Credential{}, fmt.Errorf("dlrgate: generate token: %w", err)
	}
	token := TokenPrefix + base64.RawURLEncoding.EncodeToString(tokenRaw)
	return Credential{
		KeyID:       hex.EncodeToString(idRaw),
		Token:       token,
		TokenSHA256: TokenDigest(token),
	}, nil
}

// TokenDigest is the stored proof of a token: hex SHA-256 of the presented
// bytes, never the secret itself.
//
// Unsalted and uniterated on purpose, for the same reason the message-pull
// credential is: the input is 256 bits from crypto/rand, so there is no
// dictionary to precompute and no work factor that would change the outcome.
func TokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ValidKeyID reports whether id is a well-formed key id.
func ValidKeyID(id string) bool { return keyIDPattern.MatchString(id) }

// TokenMatches compares a presented token against a stored digest in constant
// time. A missing digest never matches: a user whose credential was cleared
// cannot be authenticated by presenting an empty token.
func TokenMatches(presented, storedDigest string) bool {
	if storedDigest == "" || presented == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(TokenDigest(presented)), []byte(storedDigest)) == 1
}

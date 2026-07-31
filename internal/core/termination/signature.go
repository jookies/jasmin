package termination

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// deliverySignaturePrefix is the algorithm tag the X-Synevyr-Signature header
// carries, so a future algorithm change is a new prefix rather than a silent
// reinterpretation of the same hex.
const deliverySignaturePrefix = "sha256="

// deliverySigningInput builds the exact byte sequence the HMAC covers:
//
//	<timestamp> "." <raw body>
//
// The separator is not decoration. Without it, "1785412800" + "0{...}" and
// "17854128000" + "{...}" sign identical bytes, so a receiver that splits the
// two differently than the sender would still verify — the signature would stop
// binding the timestamp to the body it was sent with, which is the whole point
// of covering the timestamp. The form matches the Stripe/Twilio convention the
// delivery contract points at.
//
// The body must be the bytes actually written to the request, never a
// re-marshalling of the same struct.
func deliverySigningInput(timestamp string, body []byte) []byte {
	buf := make([]byte, 0, len(timestamp)+1+len(body))
	buf = append(buf, timestamp...)
	buf = append(buf, '.')
	buf = append(buf, body...)
	return buf
}

// DeliverySignature computes the X-Synevyr-Signature header value for a
// delivery: HMAC-SHA256 over the timestamp and the raw body, keyed by the
// per-connector secret, rendered as "sha256=" plus lowercase hex.
func DeliverySignature(secret []byte, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	// hash.Hash.Write never returns an error.
	_, _ = mac.Write(deliverySigningInput(timestamp, body))
	return deliverySignaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// DeliveryVerifySignature reports whether header is the signature this gateway
// would produce for the given timestamp and body.
//
// It exists so the gateway's own tests and any in-tree receiver check the
// signature the same way a downstream application must, rather than
// re-deriving the scheme. The comparison is constant time; freshness of the
// timestamp is the receiver's policy and is deliberately not decided here.
func DeliveryVerifySignature(secret []byte, timestamp string, body []byte, header string) bool {
	expected := DeliverySignature(secret, timestamp, body)
	return hmac.Equal([]byte(expected), []byte(header))
}

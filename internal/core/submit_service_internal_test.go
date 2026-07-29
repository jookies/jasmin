package core

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestRandomReferenceNeverReturnsZero(t *testing.T) {
	original := rand.Reader
	rand.Reader = bytes.NewReader([]byte{0, 0})
	t.Cleanup(func() { rand.Reader = original })

	reference, err := randomReference()
	if err != nil {
		t.Fatal(err)
	}
	if reference == 0 {
		t.Fatal("randomReference returned zero, which multipart segmentation rejects")
	}
}

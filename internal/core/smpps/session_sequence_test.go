package smpps

import "testing"

func TestNextSequenceWrapsAfterSMPPMaximum(t *testing.T) {
	session := &Session{outSequence: maxSequenceNumber - 1}

	if got := session.nextSequence(); got != maxSequenceNumber {
		t.Fatalf("last sequence = %#x, want %#x", got, maxSequenceNumber)
	}
	if got := session.nextSequence(); got != 1 {
		t.Fatalf("wrapped sequence = %#x, want 1", got)
	}
}

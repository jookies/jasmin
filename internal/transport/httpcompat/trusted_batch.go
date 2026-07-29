package httpcompat

import "context"

type trustedBatchKey struct{}

type trustedBatchSubmit struct {
	username  string
	digest    []byte
	messageID string
}

// WithTrustedBatchSubmit marks an in-process request recovered from the
// durable REST batch store. The context key is private to this package, so a
// network caller cannot manufacture the marker. The current credential digest
// and enabled state are still checked by the handler before submission.
func WithTrustedBatchSubmit(
	ctx context.Context,
	username string,
	digest []byte,
	messageID string,
) context.Context {
	value := trustedBatchSubmit{
		username: username, digest: append([]byte(nil), digest...), messageID: messageID,
	}
	return context.WithValue(ctx, trustedBatchKey{}, value)
}

func batchSubmitFromContext(ctx context.Context, username string) (trustedBatchSubmit, bool) {
	value, ok := ctx.Value(trustedBatchKey{}).(trustedBatchSubmit)
	if !ok || value.username == "" || value.username != username || value.messageID == "" {
		return trustedBatchSubmit{}, false
	}
	value.digest = append([]byte(nil), value.digest...)
	return value, true
}

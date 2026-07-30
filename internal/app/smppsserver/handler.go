package smppsserver

import (
	"context"

	"github.com/pumpitspace/synevyr/internal/app/smppssubmit"
	"github.com/pumpitspace/synevyr/internal/core"
	"github.com/pumpitspace/synevyr/internal/core/smpps"
)

// newSubmitHandler builds the submit-ingestion handler over the directory's
// credentials and the shared MT submitter. The submitter is wrapped so the
// user's default source address is applied between credential validation and
// routing — the same point legacy applies updatePDUWithUserDefaults
// (jasmin/protocols/smpp/factory.py: validation sees the raw PDU, the router
// sees the defaulted one).
func newSubmitHandler(directory *Directory, submitter core.Submitter) (smpps.SubmitHandler, error) {
	return smppssubmit.NewHandler(directory, &defaultSourceSubmitter{
		directory: directory,
		next:      submitter,
	})
}

// defaultSourceSubmitter substitutes the credential's default source address
// for an absent or empty source_addr, per ApplyDefaultSourceAddressSubmit's
// smpps semantics (legacy treats a zero-length source_addr as needing the
// default, unlike the HTTP path which only replaces an absent one). It lives
// here rather than in smppssubmit so the directory stays the single owner of
// credential state.
type defaultSourceSubmitter struct {
	directory *Directory
	next      core.Submitter
}

func (s *defaultSourceSubmitter) Submit(ctx context.Context, request core.SubmitRequest) (string, error) {
	if request.From == "" {
		if credential, ok := s.directory.ResolveCredential(request.Username); ok {
			if def, has := credential.DefaultSourceAddress(); has {
				request.From = string(def)
			}
		}
	}
	return s.next.Submit(ctx, request)
}

package smppsserver

import (
	"github.com/pumpitspace/jasmin/internal/app/smppssubmit"
	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/smpps"
)

// newSubmitHandler builds the submit-ingestion handler over the directory's
// credentials and the shared MT submitter.
func newSubmitHandler(directory *Directory, submitter core.Submitter) (smpps.SubmitHandler, error) {
	return smppssubmit.NewHandler(directory, submitter)
}

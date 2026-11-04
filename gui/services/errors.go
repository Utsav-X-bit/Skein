package services

import (
	"errors"

	"github.com/Utsav-X-bit/skein/internal/ctlerr"
)

// Step 8 three-state classification (plan §8.1/§5), shared with the CLI
// through internal/ctlerr. The GUI services return APIError values as
// before (binding signatures unchanged); callers classify them with
// ClassifyError instead of treating every failure as a generic toast:
//
//   - Unauthenticated (401): the daemon did not recognise this user.
//   - Denied (403): recognised, but the role is too low (or the
//     transport may not carry the endpoint). Do not retry unchanged.
//   - DaemonDown: ErrDaemonNotConnected — no control transport
//     answered. Start the service.
//   - Locked (423): another daemon holds the config.
//
// APIError.Kind reports the class; APIError.ClassifiedRemediation
// returns the daemon's remediation when it gave one, else the Kind's.
func (e *APIError) Kind() ctlerr.Kind {
	if e == nil {
		return ctlerr.Other
	}
	return ctlerr.ClassifyStatus(e.Status)
}

// ClassifyError maps any service-layer error to its Kind.
func ClassifyError(err error) ctlerr.Kind {
	if err == nil {
		return ctlerr.Other
	}
	if errors.Is(err, ErrDaemonNotConnected) {
		return ctlerr.DaemonDown
	}
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Kind()
	}
	return ctlerr.Other
}

// ClassifiedRemediation returns the remediation to show for err: the
// daemon's own remediation when present, else the Kind's distinct one.
func ClassifiedRemediation(err error) string {
	var ae *APIError
	if errors.As(err, &ae) && ae.Remediation != "" {
		return ae.Remediation
	}
	return ctlerr.Remediation(ClassifyError(err))
}

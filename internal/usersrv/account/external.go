package account

import "errors"

// Fixed external-auth domain errors for the later adapter/transaction stages.
// They never include provider responses, external subjects, email or ownership.
var (
	ErrProviderDisabled        = errors.New("external provider is disabled")
	ErrProviderRejected        = errors.New("external authentication failed")
	ErrIdentityConflict        = errors.New("external identity is unavailable")
	ErrIdentityLimitReached    = errors.New("provider identity limit reached")
	ErrRegistrationUnavailable = errors.New("registration is unavailable")
	ErrLastCredential          = errors.New("cannot remove the last login method")
	ErrMutationReplayMismatch  = errors.New("mutation request does not match")
	ErrChallengeInvalid        = errors.New("operation challenge is invalid")
)

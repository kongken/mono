package application

import (
	"fmt"
	"testing"

	"github.com/kongken/mono/internal/usersrv/account"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestExternalErrorsCarryFixedDomainDetails(t *testing.T) {
	for _, tc := range []struct {
		err    error
		code   codes.Code
		reason string
	}{
		{account.ErrProviderDisabled, codes.FailedPrecondition, "PROVIDER_DISABLED"},
		{account.ErrProviderRejected, codes.Unauthenticated, "PROVIDER_REJECTED"},
		{account.ErrIdentityConflict, codes.AlreadyExists, "IDENTITY_CONFLICT"},
		{account.ErrIdentityLimitReached, codes.FailedPrecondition, "IDENTITY_LIMIT_REACHED"},
		{account.ErrRegistrationUnavailable, codes.FailedPrecondition, "REGISTRATION_UNAVAILABLE"},
		{account.ErrLastCredential, codes.FailedPrecondition, "LAST_CREDENTIAL"},
		{account.ErrMutationReplayMismatch, codes.Aborted, "MUTATION_REPLAY_MISMATCH"},
		{account.ErrChallengeInvalid, codes.FailedPrecondition, "CHALLENGE_INVALID"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			err := toStatus("external", fmt.Errorf("provider private response: %w", tc.err))
			st := status.Convert(err)
			if st.Code() != tc.code || len(st.Details()) != 1 {
				t.Fatalf("status: %v", err)
			}
			info, ok := st.Details()[0].(*errdetails.ErrorInfo)
			if !ok || info.GetDomain() != "usersrv.v1" || info.GetReason() != tc.reason || len(info.GetMetadata()) != 0 || st.Message() != tc.err.Error() {
				t.Fatalf("unexpected public detail: %v", err)
			}
		})
	}
}

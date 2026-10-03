package conformance

import (
	"testing"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var errorStatuses = map[runtimev1.RuntimeErrorCode]codes.Code{
	runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_UNKNOWN:              codes.Unknown,
	runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_NOT_FOUND:            codes.NotFound,
	runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_ALREADY_EXISTS:       codes.AlreadyExists,
	runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_INVALID_ARGUMENT:     codes.InvalidArgument,
	runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_UNSUPPORTED:          codes.Unimplemented,
	runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_UNAVAILABLE:          codes.Unavailable,
	runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_FAILED_PRECONDITION:  codes.FailedPrecondition,
	runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_PERMISSION_DENIED:    codes.PermissionDenied,
	runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_TIMEOUT:              codes.DeadlineExceeded,
	runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_CANCELLED:            codes.Canceled,
	runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_RUNTIME_FAILURE:      codes.Internal,
	runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_INCOMPATIBLE_VERSION: codes.FailedPrecondition,
}

// RequireRuntimeError checks canonical status and typed runtime category, and
// returns the detail for additional retryability and diagnostic assertions.
// Use this for runtime-specific fault injection in addition to Run.
func RequireRuntimeError(
	t *testing.T,
	err error,
	category runtimev1.RuntimeErrorCode,
) *runtimev1.RuntimeError {
	t.Helper()
	code, ok := errorStatuses[category]
	if !ok {
		t.Fatalf("unknown runtime error category %v", category)
	}
	st := status.Convert(err)
	if st.Code() != code {
		t.Fatalf("got %v, want %v", err, code)
	}
	found := runtimeDetail(t, st)
	if found == nil || found.GetCode() != category {
		t.Fatalf("missing RuntimeError category %v", category)
	}
	if found.GetMessage() == "" {
		t.Fatal("runtime error has no actionable message")
	}
	return found
}

func runtimeDetail(t *testing.T, st *status.Status) *runtimev1.RuntimeError {
	t.Helper()
	var found *runtimev1.RuntimeError
	for _, detail := range st.Details() {
		runtimeErr, ok := detail.(*runtimev1.RuntimeError)
		if !ok {
			continue
		}
		if found != nil {
			t.Fatal("multiple RuntimeError details")
		}
		found = runtimeErr
	}
	return found
}

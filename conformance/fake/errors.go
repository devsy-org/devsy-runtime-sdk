package fake

import (
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var injectedStatuses = map[runtimev1.RuntimeErrorCode]codes.Code{
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

func injectedError(argv []string) error {
	if len(argv) != 2 {
		return status.Error(codes.InvalidArgument, "error requires a category")
	}
	value, ok := runtimev1.RuntimeErrorCode_value["RUNTIME_ERROR_CODE_"+argv[1]]
	if !ok {
		return status.Error(codes.InvalidArgument, "unknown injected category")
	}
	category := runtimev1.RuntimeErrorCode(value)
	code := injectedStatuses[category]
	st, err := status.New(code, "injected fixture failure").WithDetails(&runtimev1.RuntimeError{
		Code: category, Message: "injected fixture failure", RuntimeMessage: "backend diagnostic",
		Retryable: code == codes.Unavailable,
		Details:   map[string]string{"operation": "fixture"},
	})
	if err != nil {
		return status.Error(codes.Internal, "cannot encode injected failure")
	}
	return st.Err()
}

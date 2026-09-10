package app

import "errors"

type processExitError struct {
	err  error
	code int
}

func (e processExitError) Error() string { return e.err.Error() }
func (e processExitError) Unwrap() error { return e.err }

// ExitCode maps bounded operator-command outcomes to stable process statuses.
// HTTP startup and ordinary command failures retain the conventional status 1.
func ExitCode(err error) int {
	var processError processExitError
	if errors.As(err, &processError) {
		return processError.code
	}
	return 1
}

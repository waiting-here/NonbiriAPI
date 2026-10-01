package egress

import (
	"context"
	"errors"
	"net"
)

type FailureClass string

const (
	FailurePlatform FailureClass = "platform"
	FailureCanceled FailureClass = "canceled"
	FailureNetwork  FailureClass = "network"
	FailureTimeout  FailureClass = "timeout"
)

type executionError struct {
	class FailureClass
	cause error
}

func (e *executionError) Error() string { return e.cause.Error() }
func (e *executionError) Unwrap() error { return e.cause }
func ExecutionFailure(err error) FailureClass {
	var e *executionError
	if errors.As(err, &e) {
		return e.class
	}
	return ""
}
func executionFailure(class FailureClass, err error) error {
	if err == nil {
		return nil
	}
	if ExecutionFailure(err) != "" {
		return err
	}
	return &executionError{class: class, cause: err}
}
func networkFailure(err error) error {
	if errors.Is(err, context.Canceled) {
		return executionFailure(FailureCanceled, err)
	}
	class := FailureNetwork
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		class = FailureTimeout
	}
	return executionFailure(class, err)
}

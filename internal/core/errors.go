package core

import (
	"errors"
	"fmt"
)

// ErrKind classifies App errors so front-ends can decide how to surface them:
// the web UI maps kinds to HTTP status codes, a native shell might pick a
// dialog vs. a toast. Plain (unkinded) errors classify as ErrInternal.
type ErrKind int

const (
	// ErrInternal is a local failure: file IO, tunnel spawn, sshd install.
	ErrInternal ErrKind = iota
	// ErrInvalid rejects bad user input or a request the current state forbids.
	ErrInvalid
	// ErrNotFound means the referenced host id does not exist.
	ErrNotFound
	// ErrUnavailable means the capability is not wired up (nil Provisioner).
	ErrUnavailable
	// ErrRemote is a failure on, or reaching, the remote server.
	ErrRemote
)

// Error carries an ErrKind alongside the underlying error.
type Error struct {
	Kind ErrKind
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// KindOf classifies any error, defaulting to ErrInternal.
func KindOf(err error) ErrKind {
	var ce *Error
	if errors.As(err, &ce) {
		return ce.Kind
	}
	return ErrInternal
}

// errf builds a kinded error from a format string.
func errf(kind ErrKind, format string, args ...any) error {
	return &Error{Kind: kind, Err: fmt.Errorf(format, args...)}
}

// wrap attaches a kind to err; nil stays nil.
func wrap(kind ErrKind, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Kind: kind, Err: err}
}

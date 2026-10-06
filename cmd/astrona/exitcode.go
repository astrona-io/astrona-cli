package main

import "errors"

// Exit codes, so CI can tell "the solution is wrong" from "astrona or the
// setup broke".
const (
	exitError     = 1 // astrona, the lab's setup or the environment failed
	exitNotPassed = 2 // graded, and the lab didn't pass
)

// notPassedError is a grading result, not a failure of astrona: the lab
// was graded and didn't pass. main exits with exitNotPassed for it.
type notPassedError struct{ msg string }

func (e *notPassedError) Error() string { return e.msg }

func notPassed(msg string) error { return &notPassedError{msg} }

// exitCodeFor is the process exit code for a command's error.
func exitCodeFor(err error) int {
	var np *notPassedError
	if errors.As(err, &np) {
		return exitNotPassed
	}
	return exitError
}

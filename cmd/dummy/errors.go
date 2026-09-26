package main

import (
	"errors"
	"fmt"
)

var errUsage = errors.New("invalid command usage")

type usageError string

func (e usageError) Error() string        { return string(e) }
func (e usageError) Is(target error) bool { return target == errUsage }

func usageErrorf(format string, values ...any) error {
	return usageError(fmt.Sprintf(format, values...))
}

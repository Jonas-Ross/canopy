//go:build !linux && !darwin

package procs

import "context"

func enumerate(_ context.Context) ([]Process, error) {
	return nil, ErrUnsupported
}

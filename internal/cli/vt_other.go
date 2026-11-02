//go:build !windows

package cli

func enableVT() func() { return func() {} }

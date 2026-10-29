//go:build !windows

package diagnostic

func socketFailure(error) (string, string, string) { return "", "", "" }

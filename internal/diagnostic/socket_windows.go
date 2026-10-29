package diagnostic

import (
	"errors"

	"golang.org/x/sys/windows"
)

// Winsock codes differ from the portable syscall errno constants.
func socketFailure(err error) (string, string, string) {
	switch {
	case errors.Is(err, windows.WSAECONNREFUSED):
		return "network", "connection_refused", "Connection was refused"
	case errors.Is(err, windows.WSAECONNRESET):
		return "network", "connection_reset", "Connection was reset by the peer"
	case errors.Is(err, windows.WSAECONNABORTED):
		return "network", "connection_aborted", "Connection was aborted"
	case errors.Is(err, windows.WSAETIMEDOUT):
		return "timeout", "network_timeout", "Network operation timed out"
	}
	return "", "", ""
}

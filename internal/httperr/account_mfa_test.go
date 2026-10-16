package httperr

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAccountMFAErrors(t *testing.T) {
	for _, item := range []struct {
		code   string
		status int
	}{{CodeMFARequired, 403}, {CodeMFAInvalidCode, 403}, {CodeMFASetupExpired, 409}} {
		recorder := httptest.NewRecorder()
		WriteError(WithLanguage(recorder, "zh"), New(item.code, "MFA verification is required."))
		if recorder.Code != item.status || !strings.Contains(recorder.Body.String(), item.code) || strings.Contains(recorder.Body.String(), "MFA verification") {
			t.Fatal("MFA error contract", recorder.Code, recorder.Body.String())
		}
	}
}

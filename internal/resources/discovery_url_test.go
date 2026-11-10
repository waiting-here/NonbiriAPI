package resources

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDefaultDiscoveryAddressOverflowIsInvalidRequest(t *testing.T) {
	address := "https://example.com/" + strings.Repeat("x", 4096-len("https://example.com/"))
	err := initializeKeySourceTx(t.Context(), nil, 1, 1, 1, 1, Endpoint{ConnectorType: "openai-compatible", BaseURL: address}, nil, 0)
	if !errors.Is(err, ErrDiscoveryURLTooLong) {
		t.Fatal("overflow reached storage", err)
	}
	response := httptest.NewRecorder()
	writeResourceError(response, err)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_request") || !strings.Contains(response.Body.String(), "4096") {
		t.Fatal(response.Code, response.Body.String())
	}
}

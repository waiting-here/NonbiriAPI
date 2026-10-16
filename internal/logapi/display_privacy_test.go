package logapi

import (
	"bytes"
	"net/http/httptest"
	"testing"
)

func TestLogDisplayAndExportsRedactAddressesWithoutChangingSource(t *testing.T) {
	model := "backend-192.0.2.7"
	nick := "peer 198.51.100.2"
	admin := []AdminLogRow{{ID: "1", CharityModel: &model, CallerIdentity: &CallerIdentity{DiscordNickname: &nick}}}
	user := []UserLogRow{UserSelfLogRow{ID: "1", Model: model}}
	output := httptest.NewRecorder()
	writeLogJSON(output, 200, admin)
	bodies := [][]byte{output.Body.Bytes()}
	for _, encode := range []func() ([]byte, error){
		func() ([]byte, error) { return MarshalAdminJSON(admin) },
		func() ([]byte, error) { return MarshalAdminCSV(admin) },
		func() ([]byte, error) { return MarshalUserJSON(user) },
		func() ([]byte, error) { return MarshalUserCSV(user) },
	} {
		b, err := encode()
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, b)
	}
	for _, b := range bodies {
		if bytes.Contains(b, []byte("192.0.2.7")) || bytes.Contains(b, []byte("198.51.100.2")) || !bytes.Contains(b, []byte("[redacted]")) {
			t.Fatalf("display projection: %s", b)
		}
	}
	if model != "backend-192.0.2.7" || *admin[0].CallerIdentity.DiscordNickname != nick {
		t.Fatal("source facts changed")
	}
}

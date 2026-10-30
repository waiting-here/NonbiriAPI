package donation

import (
	"errors"
	"net/http"
	"testing"
)

func TestSubmissionRejectsDeletedCredentialFromOldSelection(t *testing.T) {
	e := newDonationTestEnv(t)
	owner := e.seedUser(t, "deleted-candidate-owner", nil, false)
	_, key := e.seedEndpointKey(t, owner, 'D')
	if _, err := e.store.DB().Exec(`UPDATE credentials SET current_version_id=NULL,connection_id=NULL,note='',enabled=0,deleted_at=?,updated_at=?,revision=revision+1
WHERE id=(SELECT credential_id FROM endpoint_key_current_credentials WHERE id=?)`, e.clock.Load(), e.clock.Load(), key); err != nil {
		t.Fatal(err)
	}
	input := CreateInput{Description: "Stale selection", Keys: []CreateKeyInput{{EndpointKeyID: key}}, OwnershipAuthorized: true}
	mutation := donationMutation(t, 'D', http.MethodPost, routeDonations, nil, input)
	if _, err := e.service.Create(t.Context(), owner, mutation, input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted credential should be unavailable, not an internal error: %v", err)
	}
	var donations, memberships int
	if err := e.store.DB().QueryRow(`SELECT (SELECT count(*) FROM donations),(SELECT count(*) FROM donation_key_memberships)`).Scan(&donations, &memberships); err != nil || donations != 0 || memberships != 0 {
		t.Fatalf("rejected selection created state: donations=%d memberships=%d err=%v", donations, memberships, err)
	}
}

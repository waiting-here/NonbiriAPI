package adminapi

import (
	"errors"
	"fmt"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestDiscordGateGlobalConfigurationDefaultAndAuthority(t *testing.T) {
	store := openGenerationTwoPublicConfigStore(t)
	authorizer := &siteConfigTestFinalAuthorizer{}
	repository := newSiteConfigTestRepository(t, store, authorizer)
	config, err := repository.ReadSiteConfig(t.Context(), 1)
	if err != nil || config.Values[KeyDiscordRegisteredUserGateExempt] != false {
		t.Fatal("global default is not strict", config.Values, err)
	}
	for i, raw := range []string{"true", "false"} {
		if _, err := siteConfigPatch(t, repository, KeyDiscordRegisteredUserGateExempt, raw, fmt.Sprintf("discord-gate-setting-%03d", i)); err != nil {
			t.Fatal(err)
		}
	}
	for i, raw := range []string{`"true"`, "null", "1"} {
		if _, err := siteConfigPatch(t, repository, KeyDiscordRegisteredUserGateExempt, raw, fmt.Sprintf("discord-gate-invalid-%03d", i)); err == nil {
			t.Fatal("invalid global switch accepted", raw)
		}
	}
	authorizer.setError(authz.ErrForbidden)
	if _, err := siteConfigPatch(t, repository, KeyDiscordRegisteredUserGateExempt, "true", "discord-gate-denied-001"); !errors.Is(err, ErrSiteConfigForbidden) {
		t.Fatal("global switch lacks final authorization", err)
	}
}

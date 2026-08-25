package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUserLimitProjectionKeepsRawAndEffectiveIndependent(t *testing.T) {
	st := adminStore(t)
	user, err := st.CreateUser("limits-projection", "limits", "")
	if err != nil {
		t.Fatal(err)
	}
	defaults := UserLimitDefaults{Endpoint: 3, RPM: 40, Concurrency: DefaultUserConcurrencyLimit}
	projection := ProjectUserLimits(user, defaults)
	if projection.EndpointLimit != nil || projection.RPMLimit != nil || projection.ConcurrencyLimit != nil ||
		projection.EffectiveEndpointLimit != 3 || projection.EffectiveRPMLimit != 40 ||
		projection.EffectiveConcurrencyLimit != DefaultUserConcurrencyLimit {
		t.Fatalf("null projection=%+v", projection)
	}

	endpoint, rpm, concurrency := 9000, 100, 17
	updated, err := st.UpdateUserLimits(user.ID, UserLimitPatch{
		EndpointLimitSet: true, EndpointLimit: &endpoint,
		RPMLimitSet: true, RPMLimit: &rpm,
		ConcurrencyLimitSet: true, ConcurrencyLimit: &concurrency,
	})
	if err != nil {
		t.Fatal(err)
	}
	projection = ProjectUserLimits(updated, defaults)
	if projection.EffectiveEndpointLimit != endpoint || projection.EffectiveRPMLimit != rpm ||
		projection.EffectiveConcurrencyLimit != concurrency {
		t.Fatalf("explicit projection=%+v", projection)
	}
	// Projection owns pointer copies; mutating wire state cannot modify the
	// repository row supplied by the caller.
	*projection.EndpointLimit = 1
	if *updated.EndpointLimit != endpoint {
		t.Fatal("projection aliased repository pointer")
	}
}

func TestGetUserLimitDefaultsAndAdmissionSnapshot(t *testing.T) {
	st := adminStore(t)
	user, err := st.CreateUser("limits-admission", "limits", "")
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := st.GetUserLimitDefaults(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Endpoint != DefaultEndpointLimit || defaults.RPM <= 0 || defaults.Concurrency != DefaultUserConcurrencyLimit {
		t.Fatalf("built-in defaults=%+v", defaults)
	}
	limits, err := st.GetUserAdmissionLimits(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if limits.RPMLimitSet || limits.ConcurrencyLimit != DefaultUserConcurrencyLimit {
		t.Fatalf("null admission=%+v", limits)
	}

	if err := st.SetSiteConfigValue("default_endpoint_limit", "0"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSiteConfigValue("default_rpm_per_user", "7"); err != nil {
		t.Fatal(err)
	}
	defaults, err = st.GetUserLimitDefaults(context.Background())
	if err != nil || defaults.Endpoint != 0 || defaults.RPM != 7 || defaults.Concurrency != 5 {
		t.Fatalf("configured defaults=%+v err=%v", defaults, err)
	}

	rpm, concurrency := 100, 23
	if _, err := st.UpdateUserLimits(user.ID, UserLimitPatch{
		RPMLimitSet: true, RPMLimit: &rpm,
		ConcurrencyLimitSet: true, ConcurrencyLimit: &concurrency,
	}); err != nil {
		t.Fatal(err)
	}
	limits, err = st.GetUserAdmissionLimits(context.Background(), user.ID)
	if err != nil || !limits.RPMLimitSet || limits.RPMLimit != 100 || limits.ConcurrencyLimit != 23 {
		t.Fatalf("explicit admission=%+v err=%v", limits, err)
	}

	if err := st.BanUser(user.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetUserAdmissionLimits(context.Background(), user.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("banned admission err=%v", err)
	}
	// An expired deadline ban has the same active semantics as CallerKey auth,
	// even if its lazy cleanup has not yet rewritten the stored row.
	if _, err := st.DB().Exec(`UPDATE users SET banned_until=? WHERE id=?`, time.Now().Unix()-1, user.ID); err != nil {
		t.Fatal(err)
	}
	if limits, err := st.GetUserAdmissionLimits(context.Background(), user.ID); err != nil || limits.ConcurrencyLimit != concurrency {
		t.Fatalf("expired deadline admission=%+v err=%v", limits, err)
	}
}

func TestGetUserLimitDefaultsRejectsCorruptStoredValue(t *testing.T) {
	st := adminStore(t)
	for _, tc := range []struct {
		key, value string
	}{
		{"default_endpoint_limit", "01"},
		{"default_endpoint_limit", "10001"},
		{"default_rpm_per_user", "0"},
		{"default_rpm_per_user", "4097"},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			if _, err := st.DB().Exec(`INSERT INTO site_config(key,value) VALUES(?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`, tc.key, tc.value); err != nil {
				t.Fatal(err)
			}
			if _, err := st.GetUserLimitDefaults(context.Background()); !errors.Is(err, ErrInvalidSiteConfig) {
				t.Fatalf("err=%v", err)
			}
			if _, err := st.DB().Exec(`DELETE FROM site_config WHERE key=?`, tc.key); err != nil {
				t.Fatal(err)
			}
		})
	}
}

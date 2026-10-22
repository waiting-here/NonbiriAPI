package charityrouting

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestCandidatePagesDeduplicateSharedApprovedRuns(t *testing.T) {
	e := newRoutingTestEnv(t)
	ctx := context.Background()
	e.seedUser(t, true, nil)
	model := e.createModel(t, 'R')
	modelID, _ := parsePositiveID(model.ID)
	channel, err := db.GenerateOpaqueID("mch_")
	if err != nil {
		t.Fatal(err)
	}
	seedRoutingChannel(t, e, channel, "Shared catalog", "https://shared.routing.test/v1")
	donationID, keyID, physical := e.seedCandidateWithConnector(t, e.caller, "shared", "old-run", contract.TypeOpenAICompatible, channel)
	otherDonationID, otherKeyID, _ := e.seedCandidate(t, e.caller, 'o', "outside")
	tx, err := e.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	first, err := testseed.SeedDiscoveryCatalogTx(ctx, tx, testseed.DiscoveryCatalogInput{
		EndpointKeyID: physical, ListFormat: "openai", At: routingTestNow,
		Models: []testseed.DiscoveryModel{{ID: "m"}, {ID: "m"}, {ID: "a"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var credentialVersion int64
	var fingerprint []byte
	if err := tx.QueryRowContext(ctx, `SELECT g.credential_version_id,g.report_fingerprint FROM endpoint_keys k JOIN endpoint_key_grant_revisions g ON g.id=k.current_grant_revision_id WHERE k.id=?`, physical).Scan(&credentialVersion, &fingerprint); err != nil {
		t.Fatal(err)
	}
	endpoint, err := testseed.InsertEndpointTx(ctx, tx, testseed.EndpointInput{
		OwnerUserID: e.caller, Type: contract.TypeOpenAICompatible,
		BaseURL: "https://second-source.routing.test/v1", At: routingTestNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	shared, err := testseed.InsertEndpointKeyTx(ctx, tx, testseed.KeyInput{
		EndpointID: endpoint, CredentialVersionID: credentialVersion, Fingerprint: fingerprint, At: routingTestNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := testseed.SeedDiscoveryCatalogTx(ctx, tx, testseed.DiscoveryCatalogInput{
		EndpointKeyID: shared.KeyID, ListFormat: "openai", At: routingTestNow,
		Models: []testseed.DiscoveryModel{{ID: "m"}, {ID: "b"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	images, err := contract.ParseNativeServices([]contract.NativeService{contract.NativeServiceImagesGenerations})
	if err != nil {
		t.Fatal(err)
	}
	var useID, oldRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT id,current_approved_revision_id FROM donation_uses WHERE donation_key_id=? AND native_service_bit=?`, keyID, images.Mask()).Scan(&useID, &oldRevision); err != nil {
		t.Fatal(err)
	}
	// Chat and embeddings retain the first source. Images shares a second
	// source authorized by the same credential version.
	result, err := tx.ExecContext(ctx, `INSERT INTO donation_use_revisions(
use_id,use_revision,grant_revision_id,discovery_source_revision_id,source_grant_revision_id,source_discovery_revision_id,
credential_id_snapshot,credential_version,grant_revision,display_head,display_tail,report_fingerprint,key_body_review_hmac,
origin,review_state,created_at,reviewed_at)
SELECT use_id,use_revision+1,grant_revision_id,?1,source_grant_revision_id,?1,
credential_id_snapshot,credential_version,grant_revision,display_head,display_tail,report_fingerprint,key_body_review_hmac,
'legacy','approved',?2,?2 FROM donation_use_revisions WHERE id=?3`, second.SourceRevisionID, routingTestNow, oldRevision)
	if err != nil {
		t.Fatal(err)
	}
	newRevision, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE donation_uses SET current_approved_revision_id=?,revision=revision+1 WHERE id=?`, newRevision, useID); err != nil {
		t.Fatal(err)
	}
	var firstUses, secondUses int
	if err := tx.QueryRowContext(ctx, `SELECT SUM(ur.discovery_source_revision_id=?1),SUM(ur.discovery_source_revision_id=?2)
FROM donation_uses u JOIN donation_use_revisions ur ON ur.id=u.current_approved_revision_id
WHERE u.donation_key_id=?3`, first.SourceRevisionID, second.SourceRevisionID, keyID).Scan(&firstUses, &secondUses); err != nil || firstUses != 2 || secondUses != 1 {
		t.Fatalf("approved source fixture: first=%d second=%d err=%v", firstUses, secondUses, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM donation_key_manual_models WHERE donation_key_id=?`, keyID); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"m", "c"} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO donation_key_manual_models(donation_key_id,normalized_model_id,display_name,revision,created_at,updated_at) VALUES(?1,?2,?2,1,?3,?3)`, keyID, name, routingTestNow); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	read := func(t *testing.T, query CandidateQuery) []string {
		t.Helper()
		selection, args := candidatePageSelection(modelID, routingTestNow, query)
		rows, err := e.store.DB().QueryContext(ctx, selection+` ORDER BY dk.id,pc.normalized_model_id`, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var got []string
		for rows.Next() {
			var candidateKey, donation, automatic, manual, concurrency, rpm int64
			var kind, base, head, tail, name string
			if err := rows.Scan(&candidateKey, &donation, &kind, &base, &head, &tail, &name, &automatic, &manual, &concurrency, &rpm); err != nil {
				t.Fatal(err)
			}
			if candidateKey != keyID || donation != donationID {
				t.Fatalf("unrelated candidate: key=%d donation=%d", candidateKey, donation)
			}
			got = append(got, name+":"+strings.Join(sourceTypes(automatic, manual), ","))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return got
	}
	all := []string{"a:automatic", "b:automatic", "c:manual", "m:automatic,manual"}
	for _, tc := range []struct {
		name  string
		query CandidateQuery
		want  []string
	}{
		{"key", CandidateQuery{DonationKeyID: keyID}, all},
		{"donation", CandidateQuery{DonationID: donationID}, all},
		{"both", CandidateQuery{DonationID: donationID, DonationKeyID: keyID}, all},
		{"mismatched", CandidateQuery{DonationID: otherDonationID, DonationKeyID: keyID}, nil},
		{"automatic", CandidateQuery{DonationKeyID: keyID, Source: "automatic"}, []string{all[0], all[1], all[3]}},
		{"manual", CandidateQuery{DonationKeyID: keyID, Source: "manual"}, []string{all[2], all[3]}},
		{"search", CandidateQuery{DonationKeyID: keyID, Query: "m"}, []string{all[3]}},
		{"literal wildcard", CandidateQuery{DonationKeyID: keyID, Query: "%"}, nil},
		{"trainee mainstream", CandidateQuery{DonationKeyID: keyID, traineeOnly: true}, all},
		{"trainee custom", CandidateQuery{DonationKeyID: otherKeyID, traineeOnly: true}, nil},
		{"all parameters", CandidateQuery{DonationID: donationID, DonationKeyID: keyID, traineeOnly: true, KeyState: "available", Source: "manual", Query: "m", AfterKeyID: keyID, AfterModelID: "c"}, []string{all[3]}},
		{"after last", CandidateQuery{DonationID: donationID, DonationKeyID: keyID, AfterKeyID: keyID, AfterModelID: "m"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := read(t, tc.query); !slices.Equal(got, tc.want) {
				t.Fatalf("candidates=%v want=%v", got, tc.want)
			}
		})
	}
	query := CandidateQuery{DonationID: donationID, DonationKeyID: keyID, KeyState: "available"}
	page, err := e.service.candidatesPage(ctx, roleAdmin, 0, modelID, query, pagination.Request{Page: pagination.MaxPage, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Pagination.TotalItems != "4" || page.Pagination.Page != "1" || len(page.Data) != 4 {
		t.Fatalf("deduplicated page: %+v rows=%d", page.Pagination, len(page.Data))
	}
	for i, item := range page.Data {
		got := item.UpstreamModelID + ":" + strings.Join(item.SourceTypes, ",")
		if item.DonationKeyID != strconv.FormatInt(keyID, 10) || got != all[i] {
			t.Fatalf("page pair %d: %+v", i, item)
		}
	}
	query.Limit = 2
	firstPage, nextKey, nextModel, err := e.service.BindingCandidatesAdmin(ctx, modelID, query)
	if err != nil || len(firstPage) != 2 || firstPage[0].UpstreamModelID != "a" || firstPage[1].UpstreamModelID != "b" || nextKey != keyID || nextModel != "b" {
		t.Fatalf("first cursor page=%+v next=%d/%s err=%v", firstPage, nextKey, nextModel, err)
	}
	query.AfterKeyID, query.AfterModelID = nextKey, nextModel
	lastPage, nextKey, nextModel, err := e.service.BindingCandidatesAdmin(ctx, modelID, query)
	if err != nil || len(lastPage) != 2 || lastPage[0].UpstreamModelID != "c" || lastPage[1].UpstreamModelID != "m" || nextKey != 0 || nextModel != "" {
		t.Fatalf("last cursor page=%+v next=%d/%s err=%v", lastPage, nextKey, nextModel, err)
	}
	e.seedBinding(t, modelID, keyID, "m", 0)
	if got := read(t, CandidateQuery{DonationID: donationID, DonationKeyID: keyID}); !slices.Equal(got, all[:3]) {
		t.Fatalf("bound pair remained: %v", got)
	}
	if got := read(t, CandidateQuery{DonationID: donationID, DonationKeyID: keyID, IncludeBound: true}); !slices.Equal(got, all) {
		t.Fatalf("bound pair missing from explicit inclusion: %v", got)
	}
}

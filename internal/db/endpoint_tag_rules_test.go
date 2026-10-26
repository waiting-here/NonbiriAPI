package db

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func restoreSchema38LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, name := range []string{"effective_endpoint_tags", "endpoint_tag_rule_matches"} {
		hostileMustExec(t, database, "DROP VIEW "+name)
	}
	for _, name := range []string{"endpoint_tag_domains_endpoint_insert", "endpoint_tag_domains_key_insert", "endpoint_tag_domains_manual_insert", "endpoint_tag_domains_endpoint_delete", "endpoint_tag_domains_key_delete", "endpoint_tag_domains_manual_delete"} {
		hostileMustExec(t, database, "DROP TRIGGER "+name)
	}
	for _, name := range []string{"endpoint_tag_rule_tags", "endpoint_tag_rules", "endpoint_tag_domains"} {
		hostileMustExec(t, database, "DROP TABLE "+name)
	}
	hostileMustExec(t, database, "DROP INDEX idx_donation_keys_base_url")
	hostileMustExec(t, database, EndpointTagsSQL)
}

func TestEndpointTagRulePrecedenceRestorationAndIndexedReads(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	base := "https://a.foo.example.com:8443/v1"
	exec(`INSERT INTO admin_endpoint_tags VALUES(?,'mysterious')`, base)
	read := func(want ...string) {
		t.Helper()
		rows, err := database.Query(`SELECT tag FROM effective_endpoint_tags WHERE base_url=? ORDER BY tag`, base)
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			got = append(got, value)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) {
			t.Fatal(got, want)
		}
	}
	read("mysterious")
	for i := range 100 {
		exec(`INSERT INTO admin_endpoint_tags VALUES(?,'mysterious')`, fmt.Sprintf("https://unrelated-%d.test/v1", i))
	}
	exec(`INSERT INTO endpoint_tag_rules VALUES('example.com','add')`)
	exec(`INSERT INTO endpoint_tag_rule_tags VALUES('example.com','mainstream')`)
	exec(`INSERT INTO endpoint_tag_rule_tags VALUES('example.com','mysterious')`)
	read("mainstream", "mysterious")
	exec(`INSERT INTO endpoint_tag_rules VALUES('foo.example.com','add')`)
	exec(`INSERT INTO endpoint_tag_rule_tags VALUES('foo.example.com','mainstream')`)
	read("mainstream", "mysterious")
	exec(`DELETE FROM endpoint_tag_rule_tags WHERE domain='foo.example.com'`)
	exec(`UPDATE endpoint_tag_rules SET action='remove' WHERE domain='foo.example.com'`)
	read()
	exec(`INSERT INTO endpoint_tag_rules VALUES('a.foo.example.com','add')`)
	exec(`INSERT INTO endpoint_tag_rule_tags VALUES('a.foo.example.com','other_third_party')`)
	read("other_third_party")
	exec(`DELETE FROM endpoint_tag_rule_tags WHERE domain='a.foo.example.com'`)
	exec(`DELETE FROM endpoint_tag_rules WHERE domain IN ('a.foo.example.com','foo.example.com')`)
	read("mainstream", "mysterious")
	exec(`DELETE FROM endpoint_tag_rule_tags`)
	exec(`DELETE FROM endpoint_tag_rules`)
	read("mysterious")
	plans := func(label string) {
		t.Helper()
		t.Log(label)
		for _, query := range []string{
			`SELECT EXISTS(SELECT 1 FROM effective_endpoint_tags WHERE base_url=? AND tag='mainstream')`,
			`SELECT id FROM donation_keys k WHERE k.donation_id=? AND EXISTS(SELECT 1 FROM effective_endpoint_tags t WHERE t.base_url=k.canonical_base_url AND t.tag='mainstream')`,
			`SELECT id FROM donation_keys k WHERE k.donation_id=? AND EXISTS(SELECT 1 FROM effective_endpoint_tags t WHERE t.base_url=k.canonical_base_url)`,
		} {
			rows, err := database.Query(`EXPLAIN QUERY PLAN `+query, base)
			if err != nil {
				t.Fatal(err)
			}
			var plan strings.Builder
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				plan.WriteString(detail + "\n")
			}
			rows.Close()
			t.Log(query, "\n", plan.String())
			if strings.Contains(plan.String(), "SCAN m") || strings.Contains(plan.String(), "SCAN d") || !strings.Contains(plan.String(), "SEARCH m USING PRIMARY KEY (base_url=?") || !strings.Contains(plan.String(), "SEARCH d USING PRIMARY KEY (base_url=?") {
				t.Fatal("qualification scanned unrelated addresses", plan.String())
			}
		}
	}
	plans("100 unrelated addresses, zero rules")
	exec(`INSERT INTO endpoint_tag_rules VALUES('example.com','add'),('unrelated-1.test','remove')`)
	exec(`INSERT INTO endpoint_tag_rule_tags VALUES('example.com','mainstream')`)
	read("mainstream", "mysterious")
	plans("100 unrelated addresses, two rules")
	exec(`DELETE FROM admin_endpoint_tags WHERE base_url=?`, base)
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM endpoint_tag_domains WHERE base_url=?`, base).Scan(&count); err != nil || count != 0 {
		t.Fatal("derived address outlived last source", count, err)
	}
}

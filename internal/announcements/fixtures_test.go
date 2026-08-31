package announcements

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAnnouncementDTOFixtures(t *testing.T) {
	expiresAt := int64(1_700_003_600)
	fallback := "zh"
	zhTitle, zhBody, oldTitle, oldRendered, previewBody := "标题", "正文", "旧标题", "<p>旧正文</p>", "<p>正文</p>"
	cases := []struct {
		name string
		file string
		got  any
	}{
		{
			name: "user page", file: "user_page.json",
			got: Page[AnnouncementSummary]{Data: []AnnouncementSummary{{
				Epoch: "b1e_AAAAAAAAAAAAAAAAAAAAAQ", ID: "ann_AAAAAAAAAAAAAAAAAAAAAQ", Revision: "2",
				Severity: "important", Pinned: true, Dismissible: false, PublishedAt: 1_700_000_000,
				ExpiresAt: &expiresAt, EffectiveLanguage: "en", Title: "Title", Excerpt: "Plain excerpt",
			}}},
		},
		{
			name: "user detail", file: "user_detail.json",
			got: AnnouncementDetail{
				Epoch: "b1e_AAAAAAAAAAAAAAAAAAAAAQ", ID: "ann_AAAAAAAAAAAAAAAAAAAAAQ", Revision: "2",
				Severity: "important", Pinned: true, Dismissible: false, PublishedAt: 1_700_000_000,
				ExpiresAt: &expiresAt, EffectiveLanguage: "en", FallbackFrom: &fallback,
				Title: "Title", RenderedBody: "<p>Body</p>",
			},
		},
		{
			name: "admin announcement", file: "admin_announcement.json",
			got: AdminAnnouncement{
				ID: "ann_AAAAAAAAAAAAAAAAAAAAAQ", State: "published", Revision: "3",
				Draft: AnnouncementDraftLanguages{ZH: &AnnouncementLanguageDraft{Title: zhTitle, Body: zhBody}},
				Published: &AnnouncementPublished{Revision: "2", PublishedAt: 1_700_000_000,
					ZH: &AnnouncementLanguagePublished{Title: oldTitle, RenderedBody: oldRendered}},
				Severity: "warning", Dismissible: true, CreatedAt: 1_699_999_900, UpdatedAt: 1_700_000_010,
			},
		},
		{name: "mutation receipt", file: "mutation_receipt.json", got: AnnouncementMutationReceipt{
			ID: "ann_AAAAAAAAAAAAAAAAAAAAAQ", Revision: "3",
		}},
		{name: "preview", file: "preview.json", got: Preview{RenderedZH: &previewBody, RenderProfileVersion: RenderProfileVersion}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			actual, err := json.Marshal(testCase.got)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			expected, err := os.ReadFile(filepath.Join("testdata", testCase.file))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			var actualValue, expectedValue any
			if err := json.Unmarshal(actual, &actualValue); err != nil {
				t.Fatalf("decode actual: %v", err)
			}
			if err := json.Unmarshal(expected, &expectedValue); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			if !reflect.DeepEqual(actualValue, expectedValue) {
				t.Fatalf("fixture mismatch\nactual: %s\nwant:   %s", actual, expected)
			}
		})
	}
}

package announcements

const (
	RenderProfileVersion        = "announcement-markdown/v1"
	PermanentDeleteConfirmation = "DELETE"
)

type Page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

type AnnouncementSummary struct {
	Epoch             string  `json:"epoch"`
	ID                string  `json:"id"`
	Revision          string  `json:"revision"`
	Severity          string  `json:"severity"`
	Pinned            bool    `json:"pinned"`
	Dismissible       bool    `json:"dismissible"`
	PublishedAt       int64   `json:"published_at"`
	ExpiresAt         *int64  `json:"expires_at"`
	EffectiveLanguage string  `json:"effective_language"`
	FallbackFrom      *string `json:"fallback_from"`
	Title             string  `json:"title"`
	Excerpt           string  `json:"excerpt"`
}

type AnnouncementDetail struct {
	Epoch             string  `json:"epoch"`
	ID                string  `json:"id"`
	Revision          string  `json:"revision"`
	Severity          string  `json:"severity"`
	Pinned            bool    `json:"pinned"`
	Dismissible       bool    `json:"dismissible"`
	PublishedAt       int64   `json:"published_at"`
	ExpiresAt         *int64  `json:"expires_at"`
	EffectiveLanguage string  `json:"effective_language"`
	FallbackFrom      *string `json:"fallback_from"`
	Title             string  `json:"title"`
	RenderedBody      string  `json:"rendered_body"`
}

type AnnouncementLanguageDraft struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type AnnouncementLanguagePublished struct {
	Title        string `json:"title"`
	RenderedBody string `json:"rendered_body"`
}

type AnnouncementPublished struct {
	Revision    string                         `json:"revision"`
	PublishedAt int64                          `json:"published_at"`
	ZH          *AnnouncementLanguagePublished `json:"zh"`
	EN          *AnnouncementLanguagePublished `json:"en"`
}

type AdminAnnouncement struct {
	ID          string                     `json:"id"`
	State       string                     `json:"state"`
	Revision    string                     `json:"revision"`
	Draft       AnnouncementDraftLanguages `json:"draft"`
	Published   *AnnouncementPublished     `json:"published"`
	Severity    string                     `json:"severity"`
	Pinned      bool                       `json:"pinned"`
	Dismissible bool                       `json:"dismissible"`
	ExpiresAt   *int64                     `json:"expires_at"`
	WithdrawnAt *int64                     `json:"withdrawn_at"`
	CreatedAt   int64                      `json:"created_at"`
	UpdatedAt   int64                      `json:"updated_at"`
}

type AnnouncementMutationReceipt struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

type AnnouncementDraftLanguages struct {
	ZH *AnnouncementLanguageDraft `json:"zh"`
	EN *AnnouncementLanguageDraft `json:"en"`
}

type Preview struct {
	RenderedZH           *string `json:"rendered_zh"`
	RenderedEN           *string `json:"rendered_en"`
	RenderProfileVersion string  `json:"render_profile_version"`
}

// NullableTime distinguishes an omitted PATCH field from an explicit null.
type NullableTime struct {
	Set   bool
	Value *int64
}

type DraftPatch struct {
	TitleZH     *string
	BodyZH      *string
	TitleEN     *string
	BodyEN      *string
	Severity    *string
	Pinned      *bool
	Dismissible *bool
	ExpiresAt   NullableTime
}

type PreviewInput struct {
	ExpectedRevision int64
	Draft            DraftPatch
}

type ControlMutation struct {
	IdempotencyKey string
	Method         string
	Route          string
	PathIDs        []string
	Query          string
	CanonicalBody  []byte
}

type MutationResult[T any] struct {
	Value    T
	Status   int
	Body     []byte
	Replayed bool
}

type AdminListQuery struct {
	State    string
	Severity string
	Cursor   string
	Limit    int
}

type PageQuery struct {
	Cursor string
	Limit  int
}

type RecoveryResult struct {
	Expired            int
	ActorsDeidentified int
	AuditsDeleted      int
}

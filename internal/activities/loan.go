package activities

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

const loanQuotePurpose = "activity-loan-quote/v1"
const maxLoanTokenBytes = 2048

type LoanTerms struct {
	Principal string `json:"principal"`
	A         string `json:"a"`
	B         string `json:"b"`
	Nominal   string `json:"nominal"`
	Disbursed string `json:"disbursed"`
	Fee       string `json:"fee"`
	Repayment string `json:"repayment"`
	Interest  string `json:"interest"`
}

type LoanBalances struct {
	GeneralBefore string `json:"general_before"`
	GeneralAfter  string `json:"general_after"`
	GameBefore    string `json:"game_before"`
	GameAfter     string `json:"game_after"`
}

type LoanQuote struct {
	LoanTerms
	LoanBalances
	QuoteToken     string `json:"quote_token"`
	ExpiresAt      int64  `json:"expires_at"`
	ConfigRevision string `json:"config_revision"`
	AsOf           int64  `json:"as_of"`
}

type LoanReceipt struct {
	LoanTerms
	LoanBalances
	ID             string `json:"loan_id"`
	OperationID    string `json:"operation_id"`
	Sequence       string `json:"sequence"`
	CreatedAt      int64  `json:"created_at"`
	ConfigRevision string `json:"config_revision"`
}

type loanPayload struct {
	Version   int    `json:"version"`
	Nonce     string `json:"nonce"`
	Owner     string `json:"owner"`
	Tier      int    `json:"tier"`
	Revision  string `json:"revision"`
	IssuedAt  int64  `json:"issued_at"`
	ExpiresAt int64  `json:"expires_at"`
	LoanTerms
}

func projectLoanTerms(t db.LoanTerms) LoanTerms {
	return LoanTerms{strconv.FormatInt(t.Principal, 10), formatMilliPointsInt64(t.A), formatMilliPointsInt64(t.B), formatMilliPointsInt64(t.Nominal), formatMilliPointsInt64(t.Disbursed), formatMilliPointsInt64(t.Fee), formatMilliPointsInt64(t.Repayment), formatMilliPointsInt64(t.Interest)}
}

func projectLoanBalances(generalBefore, gameBefore, generalAfter, gameAfter ledger.Amount) LoanBalances {
	return LoanBalances{formatMilliPoints(generalBefore.Big()), formatMilliPoints(generalAfter.Big()), formatMilliPoints(gameBefore.Big()), formatMilliPoints(gameAfter.Big())}
}

func loanParticipantTx(ctx context.Context, tx *sql.Tx, user, now int64) error {
	var admin, banned bool
	var until sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT is_admin,is_banned,banned_until FROM users WHERE id=?`, user).Scan(&admin, &banned, &until); err != nil {
		if isNoRows(err) {
			return ErrNotFound
		}
		return classifyDatabaseError("read loan participant", err)
	}
	if admin || banned && (!until.Valid || until.Int64 > now) {
		return ErrForbidden
	}
	return nil
}

func projectLoanTx(ctx context.Context, tx *sql.Tx, user, now int64, c activityConfig, view *LoanView) error {
	*view = LoanView{Enabled: c.loan.enabled, Reason: "disabled", Tiers: append([]string{}, c.loan.tiers[:]...)}
	if !c.masterEnabled || !c.loan.enabled {
		return nil
	}
	if err := loanParticipantTx(ctx, tx, user, now); err != nil {
		if err == ErrForbidden {
			view.Reason = "ineligible"
			return nil
		}
		return err
	}
	wallet, err := ledger.UserAccount(ctx, tx, user)
	if err != nil {
		return classifyLedgerError("read loan eligibility", err)
	}
	if wallet.Balance.Sign() < 0 {
		view.Reason = "negative_balance"
		return nil
	}
	view.Available, view.Reason = true, "available"
	return nil
}

func (r *Repository) signLoan(payload loanPayload) (string, error) {
	key, err := r.cursors.keys.DeriveGenerationTwoSubkey([]byte(loanQuotePurpose))
	if err != nil || len(key) != 32 {
		return "", ErrUnavailable
	}
	defer clear(key)
	body, err := json.Marshal(payload)
	if err != nil {
		return "", ErrInvariant
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(body)
	token := base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(token) > maxLoanTokenBytes {
		return "", ErrInvariant
	}
	return token, nil
}

func (r *Repository) verifyLoan(token string, user, now int64) (loanPayload, error) {
	var p loanPayload
	if len(token) == 0 || len(token) > maxLoanTokenBytes {
		return p, ErrInvalidRequest
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return p, ErrInvalidRequest
	}
	body, e1 := base64.RawURLEncoding.DecodeString(parts[0])
	signature, e2 := base64.RawURLEncoding.DecodeString(parts[1])
	if e1 != nil || e2 != nil || len(signature) != sha256.Size || base64.RawURLEncoding.EncodeToString(body) != parts[0] || base64.RawURLEncoding.EncodeToString(signature) != parts[1] {
		return p, ErrInvalidRequest
	}
	key, err := r.cursors.keys.DeriveGenerationTwoSubkey([]byte(loanQuotePurpose))
	if err != nil || len(key) != 32 {
		return p, ErrUnavailable
	}
	defer clear(key)
	mac := hmac.New(sha256.New, key)
	mac.Write(body)
	if !hmac.Equal(signature, mac.Sum(nil)) || strictjson.ValidateObject(body) != nil {
		return p, ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&p) != nil {
		return p, ErrInvalidRequest
	}
	canonical, err := json.Marshal(p)
	if err != nil || !bytes.Equal(body, canonical) || p.Version != 1 || !db.ValidateOpaqueID(p.Nonce, "lqn_") || p.Owner != strconv.FormatInt(user, 10) || p.Tier < 1 || p.Tier > 3 || p.IssuedAt < 0 || p.IssuedAt > maxUnixSecond-60 || p.ExpiresAt != p.IssuedAt+60 {
		return p, ErrInvalidRequest
	}
	if now < p.IssuedAt || now >= p.ExpiresAt {
		return p, ErrConflict
	}
	return p, nil
}

func (r *Repository) QuoteLoan(ctx context.Context, user int64, tier string) (LoanQuote, error) {
	if tier != "1" && tier != "2" && tier != "3" {
		return LoanQuote{}, ErrInvalidRequest
	}
	tx, err := r.beginUserMutation(ctx, user)
	if err != nil {
		return LoanQuote{}, err
	}
	defer tx.Rollback()
	now, err := r.decisionNow()
	if err != nil {
		return LoanQuote{}, err
	}
	if err := loanParticipantTx(ctx, tx, user, now); err != nil {
		return LoanQuote{}, err
	}
	c, err := readActivityConfigTx(ctx, tx)
	if err != nil {
		return LoanQuote{}, err
	}
	if !c.masterEnabled || !c.loan.enabled {
		return LoanQuote{}, ErrFeatureDisabled
	}
	general, err := ledger.UserAccount(ctx, tx, user)
	if err != nil {
		return LoanQuote{}, classifyLedgerError("read loan general wallet", err)
	}
	game, err := ledger.UserAssetAccount(ctx, tx, user, ledger.Game)
	if err != nil {
		return LoanQuote{}, classifyLedgerError("read loan game wallet", err)
	}
	if general.Balance.Sign() < 0 {
		return LoanQuote{}, ErrInsufficientCredits
	}
	tierNo := int(tier[0] - '0')
	terms, err := c.loan.terms(tierNo)
	if err != nil {
		return LoanQuote{}, ErrInvariant
	}
	generalAfter, e1 := ledger.AmountFromBig(new(big.Int).Sub(general.Balance.Big(), big.NewInt(terms.Repayment)))
	gameAfter, e2 := ledger.AmountFromBig(new(big.Int).Add(game.Balance.Big(), big.NewInt(terms.Disbursed)))
	if e1 != nil || e2 != nil {
		return LoanQuote{}, ErrResourceLimit
	}
	nonce, err := db.GenerateOpaqueID("lqn_")
	if err != nil {
		return LoanQuote{}, ErrUnavailable
	}
	p := loanPayload{Version: 1, Nonce: nonce, Owner: strconv.FormatInt(user, 10), Tier: tierNo, Revision: strconv.FormatInt(c.revision, 10), IssuedAt: now, ExpiresAt: now + 60, LoanTerms: projectLoanTerms(terms)}
	token, err := r.signLoan(p)
	if err != nil {
		return LoanQuote{}, err
	}
	if err := tx.Commit(); err != nil {
		return LoanQuote{}, classifyDatabaseError("commit loan quote read", err)
	}
	return LoanQuote{LoanTerms: p.LoanTerms, LoanBalances: projectLoanBalances(general.Balance, game.Balance, generalAfter, gameAfter), QuoteToken: token, ExpiresAt: p.ExpiresAt, ConfigRevision: p.Revision, AsOf: now}, nil
}

func (r *Repository) Borrow(ctx context.Context, user int64, mutation ControlMutation, token string) (MutationResult[LoanReceipt], PublishFacts, error) {
	var empty MutationResult[LoanReceipt]
	if len(token) == 0 || len(token) > maxLoanTokenBytes {
		return empty, PublishFacts{}, ErrInvalidRequest
	}
	tx, err := r.beginUserMutation(ctx, user)
	if err != nil {
		return empty, PublishFacts{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	now, err := r.decisionNow()
	if err != nil {
		return empty, PublishFacts{}, err
	}
	if err := loanParticipantTx(ctx, tx, user, now); err != nil {
		return empty, PublishFacts{}, err
	}
	decision, err := beginControlMutation(ctx, tx, "user", user, mutation, now)
	if err != nil {
		return empty, PublishFacts{}, err
	}
	if decision.Kind == idempotency.Replay {
		value, err := replayMutation[LoanReceipt](decision)
		if err != nil {
			return empty, PublishFacts{}, err
		}
		if err := commitTx(tx, &committed); err != nil {
			return empty, PublishFacts{}, err
		}
		return value, PublishFacts{}, nil
	}
	p, err := r.verifyLoan(token, user, now)
	if err != nil {
		return empty, PublishFacts{}, err
	}
	c, err := readActivityConfigTx(ctx, tx)
	if err != nil {
		return empty, PublishFacts{}, err
	}
	terms, err := c.loan.terms(p.Tier)
	if err != nil || p.Revision != strconv.FormatInt(c.revision, 10) || p.LoanTerms != projectLoanTerms(terms) || !c.masterEnabled || !c.loan.enabled {
		return empty, PublishFacts{}, ErrConflict
	}
	var used bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM activity_loans WHERE quote_nonce=?)`, p.Nonce).Scan(&used); err != nil {
		return empty, PublishFacts{}, classifyDatabaseError("read loan nonce", err)
	}
	if used {
		return empty, PublishFacts{}, ErrConflict
	}
	general, err := ledger.UserAccount(ctx, tx, user)
	if err != nil {
		return empty, PublishFacts{}, classifyLedgerError("read loan wallet", err)
	}
	game, err := ledger.UserAssetAccount(ctx, tx, user, ledger.Game)
	if err != nil {
		return empty, PublishFacts{}, classifyLedgerError("read loan game wallet", err)
	}
	if general.Balance.Sign() < 0 {
		return empty, PublishFacts{}, ErrInsufficientCredits
	}
	externalGeneral, err := ledger.CodedAssetAccount(ctx, tx, "external", ledger.General)
	if err != nil {
		return empty, PublishFacts{}, err
	}
	externalGame, err := ledger.CodedAssetAccount(ctx, tx, "external", ledger.Game)
	if err != nil {
		return empty, PublishFacts{}, err
	}
	op, err := generateCanonical(r.operationID, "op_")
	if err != nil {
		return empty, PublishFacts{}, err
	}
	plan, err := ledger.NewActivityLoan(ledger.Meta{OperationID: op, ActorUserID: user, CreatedAt: now}, ledger.AccountPair{General: general.ID, Game: game.ID}, ledger.AccountPair{General: externalGeneral.ID, Game: externalGame.ID}, ledger.AmountFromMilli(terms.Disbursed), ledger.AmountFromMilli(terms.Repayment))
	if err != nil {
		return empty, PublishFacts{}, classifyLedgerError("build loan", err)
	}
	posted, err := ledger.Apply(ctx, tx, plan)
	if err != nil {
		return empty, PublishFacts{}, classifyLedgerError("post loan", err)
	}
	generalAfter, err := ledger.ReadAccount(ctx, tx, general.ID)
	if err != nil {
		return empty, PublishFacts{}, err
	}
	gameAfter, err := ledger.ReadAccount(ctx, tx, game.ID)
	if err != nil {
		return empty, PublishFacts{}, err
	}
	id, err := db.GenerateOpaqueID("loan_")
	if err != nil {
		return empty, PublishFacts{}, ErrUnavailable
	}
	values := []any{id, user, p.Nonce, op, posted.LedgerSeq, now, c.revision, terms.Principal, terms.A, terms.B, terms.Nominal, terms.Disbursed, terms.Fee, terms.Repayment, terms.Interest}
	for _, amount := range []ledger.Amount{general.Balance, generalAfter.Balance, game.Balance, gameAfter.Balance} {
		scalar, err := db.SM128FromBig(amount.Big())
		if err != nil {
			return empty, PublishFacts{}, ErrInvariant
		}
		values = append(values, scalar.Sign, db.EncodeU128(scalar.Mag))
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO activity_loans(id,user_id,quote_nonce,operation_id,ledger_seq,created_at,config_revision,principal,coefficient_a,coefficient_b,nominal_milli,disbursed_milli,fee_milli,repayment_milli,interest_milli,general_before_sign,general_before_mag,general_after_sign,general_after_mag,game_before_sign,game_before_mag,game_after_sign,game_after_mag) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, values...)
	if err != nil {
		return empty, PublishFacts{}, classifyDatabaseError("record loan", err)
	}
	value := LoanReceipt{LoanTerms: projectLoanTerms(terms), LoanBalances: projectLoanBalances(general.Balance, game.Balance, generalAfter.Balance, gameAfter.Balance), ID: id, OperationID: op, Sequence: strconv.FormatInt(posted.LedgerSeq, 10), CreatedAt: now, ConfigRevision: p.Revision}
	response, err := finishJSONMutation(ctx, tx, decision, http.StatusCreated, value)
	if err != nil {
		return empty, PublishFacts{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return empty, PublishFacts{}, err
	}
	return response, PublishFacts{AccountIDs: []int64{user}}, nil
}

func (s *Service) Borrow(ctx context.Context, user int64, mutation ControlMutation, token string) (MutationResult[LoanReceipt], error) {
	result, facts, err := s.repository.Borrow(ctx, user, mutation, token)
	if err == nil {
		s.PublishCommittedFacts(ctx, facts)
	}
	return result, err
}

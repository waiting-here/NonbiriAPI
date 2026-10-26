package gacha

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func (a Action) validate() error {
	expected := Action{Action: a.Action}
	switch a.Action {
	case "enter", "check-in", "limited-check-in":
	case "draw":
		if a.Quantity != 1 && a.Quantity != 10 || a.PoolID != "permanent" && !db.ValidateOpaqueID(a.PoolID, "gp_") {
			return ErrInvalid
		}
		expected.PoolID = a.PoolID
		expected.Quantity = a.Quantity
	case "buy":
		if a.Quantity < 1 || a.Quantity > 100 || a.Ticket != "permanent" && a.Ticket != "limited" || a.Revision < 1 || a.Revision > maxBalance {
			return ErrInvalid
		}
		expected.Quantity = a.Quantity
		expected.Ticket = a.Ticket
		expected.Revision = a.Revision
	case "redeem":
		if len(a.Product) < 1 || len(a.Product) > 80 || a.Revision < 1 || a.Revision > maxBalance {
			return ErrInvalid
		}
		expected.Product = a.Product
		expected.Revision = a.Revision
	case "claim":
		if len(a.Theme) < 1 || len(a.Theme) > 80 {
			return ErrInvalid
		}
		expected.Theme = a.Theme
	default:
		return ErrInvalid
	}
	if a != expected {
		return ErrInvalid
	}
	return nil
}
func (s *Service) Act(ctx context.Context, user int64, key string, input Action) (Outcome, error) {
	var out Outcome
	if err := input.validate(); err != nil {
		return out, err
	}
	keyHash, err := idempotency.KeyHash(key)
	if err != nil {
		return out, ErrInvalid
	}
	digest, err := s.digest(input)
	if err != nil {
		return out, err
	}
	at, err := s.now()
	if err != nil {
		return out, err
	}
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.authorize(ctx, tx, user, at); err != nil {
		return out, err
	}
	var previousID, previousAction string
	var previousHash []byte
	err = tx.QueryRowContext(ctx, "SELECT id,request_hash,action FROM gacha_operations WHERE user_id=? AND key_hash=?", user, keyHash[:]).Scan(&previousID, &previousHash, &previousAction)
	if err == nil {
		if subtle.ConstantTimeCompare(previousHash, digest) != 1 {
			return out, ErrConflict
		}
		if previousAction == "draw" {
			var raw string
			err = tx.QueryRowContext(ctx, "SELECT receipt FROM gacha_draws WHERE operation_id=? AND user_id=? AND created_at>?", previousID, user, at-historyDays*86400).Scan(&raw)
			if errors.Is(err, sql.ErrNoRows) {
				out.Message = "receipt-expired"
			} else if err != nil {
				return out, err
			} else {
				out.Draw = &DrawReceipt{}
				if err = json.Unmarshal([]byte(raw), out.Draw); err != nil {
					return out, err
				}
			}
		}
		out.Snapshot, err = s.snapshot(ctx, tx, user, at)
		if err != nil {
			return out, err
		}
		return out, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err = s.config.Gate.AuthorizeUserActivity(ctx, tx, user); err != nil {
		return out, err
	}
	day, month, _, err := calendar(ctx, tx, at)
	if err != nil {
		return out, err
	}
	p, exists, err := readProfile(ctx, tx, user)
	if err != nil {
		return out, err
	}
	if input.Action == "enter" && exists && p.WelcomeGranted {
		out.Snapshot, err = s.snapshot(ctx, tx, user, at)
		if err != nil {
			return out, err
		}
		return out, tx.Commit()
	}
	if !exists {
		if input.Action != "enter" {
			return out, ErrNotFound
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO gacha_profiles(user_id,month,entered_at,updated_at) VALUES(?,?,?,?)", user, month, at, at)
		if err != nil {
			return out, err
		}
		p.Month = month
		p.EnteredAt = at
	}
	if p.Month != month {
		p.Month = month
		p.Used = [2]int64{}
	}
	before := p.Balance
	operation, err := db.GenerateOpaqueID("op_")
	if err != nil {
		return out, err
	}
	settings, err := readSettings(ctx, tx)
	if err != nil {
		return out, err
	}
	shop := settings.shop
	var creditOperation any
	var drawRules Rules
	var roster []string
	switch input.Action {
	case "enter":
		p.WelcomeGranted = true
		p.EnteredAt = at
		p.Balance[0] += 10
	case "check-in":
		if p.Day == day {
			return out, ErrClaimed
		}
		p.Day = day
		p.Balance[0]++
	case "limited-check-in":
		if p.LimitedDay == day {
			return out, ErrClaimed
		}
		p.LimitedDay = day
		p.Balance[1]++
	case "draw":
		var receipt *DrawReceipt
		receipt, drawRules, roster, err = s.draw(ctx, tx, user, at, operation, input, &p, settings)
		out.Draw = receipt
	case "buy":
		if input.Revision != shop.Revision {
			return out, ErrPrice
		}
		err = purchase(ctx, tx, user, at, operation, input, shop, &p)
		creditOperation = operation
	case "redeem":
		if input.Revision != shop.Revision {
			return out, ErrPrice
		}
		if _, bulk := bulkTicket(input.Product); bulk {
			err = purchaseBundle(ctx, tx, user, at, operation, input.Product, &p)
			creditOperation = operation
		} else {
			err = redeem(ctx, tx, user, at, input.Product, shop, &p)
		}
	case "claim":
		err = claim(ctx, tx, user, at, operation, input.Theme, &p)
		creditOperation = operation
	}
	if err != nil {
		return out, err
	}
	if err = saveProfile(ctx, tx, user, at, p); err != nil {
		return out, err
	}
	delta := [6]int64{}
	for i := range delta {
		delta[i] = p.Balance[i] - before[i]
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gacha_operations(id,user_id,key_hash,request_hash,action,delta,balance_after,credit_operation_id,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		operation, user, keyHash[:], digest, input.Action, jsonText(delta), jsonText(p.Balance), creditOperation, at)
	if err != nil {
		return out, err
	}
	if out.Draw != nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO gacha_draws(operation_id,user_id,receipt,rules,roster,catalog_version,created_at) VALUES(?,?,?,?,?,?,?)`,
			operation, user, jsonText(out.Draw), jsonText(drawRules), jsonText(roster), catalog.Version, at)
		if err != nil {
			return out, err
		}
	}
	if err = db.FreezeSiteTimezoneTx(ctx, tx, at); err != nil {
		return out, err
	}
	if s.config.Activity != nil {
		if err = s.config.Activity.RecordLimitedActivityTx(ctx, tx, user, at); err != nil {
			return out, err
		}
	}
	out.Snapshot, err = s.snapshotWithSettings(ctx, tx, user, at, settings)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Service) draw(ctx context.Context, tx *sql.Tx, user, at int64, id string, input Action, p *profile, settings *settingsSnapshot) (*DrawReceipt, Rules, []string, error) {
	pool, err := readPool(ctx, tx, input.PoolID, settings)
	if err != nil {
		return nil, Rules{}, nil, err
	}
	if !pool.Open(at) {
		return nil, Rules{}, nil, ErrClosed
	}
	wallet := 0
	if pool.Kind == "limited" {
		wallet = 1
	}
	if p.Balance[wallet] < int64(input.Quantity) {
		return nil, Rules{}, nil, ErrTickets
	}
	p.Balance[wallet] -= int64(input.Quantity)
	owned, err := readOwned(ctx, tx, user)
	if err != nil {
		return nil, Rules{}, nil, err
	}
	pities, err := readPity(ctx, tx, user)
	if err != nil {
		return nil, Rules{}, nil, err
	}
	pity, ok := pities[pool.Group]
	if !ok {
		pity = freshPity(pool.Rules)
	}
	welcome := wallet == 0 && input.Quantity == 10 && p.Newcomer
	result := &DrawReceipt{ID: id, PoolID: pool.ID, PoolName: pool.Name, CreatedAt: at, Items: []DrawItem{}}
	hasSSR := false
	for i := 0; i < input.Quantity; i++ {
		var item DrawItem
		item, pity, err = drawOne(pool.Rules, pity, pool.Featured, s.random, welcome && i == 9 && !hasSSR)
		if err != nil {
			return nil, Rules{}, nil, err
		}
		card := cardsByID[item.CardID]
		_, item.Duplicate = owned[card.ID]
		if item.Duplicate {
			for index, r := range rarities {
				if r == card.Rarity.Tier() {
					p.Balance[index+2]++
					break
				}
			}
		} else {
			_, err = tx.ExecContext(ctx, "INSERT INTO gacha_owned(user_id,card_id,obtained_at,source) VALUES(?,?,?,'draw')", user, card.ID, at)
			if err != nil {
				return nil, Rules{}, nil, err
			}
			owned[card.ID] = Owned{at, "draw"}
		}
		hasSSR = hasSSR || card.Rarity.Tier() == SSR
		result.Items = append(result.Items, item)
	}
	if welcome {
		p.Newcomer = false
	}
	if err = savePity(ctx, tx, user, pool.Group, pity); err != nil {
		return nil, Rules{}, nil, err
	}
	roster := poolRoster(pool)
	if err := recordDrawStatisticsTx(ctx, tx, user, pool, result, roster); err != nil {
		return nil, Rules{}, nil, err
	}
	return result, pool.Rules, roster, nil
}
func purchase(ctx context.Context, tx *sql.Tx, user, at int64, id string, input Action, shop ShopSettings, p *profile) error {
	unit := shop.PermanentPrice
	wallet := 0
	if input.Ticket == "limited" {
		unit = shop.LimitedPrice
		wallet = 1
	}
	value, err := price(unit)
	if err != nil {
		return err
	}
	cost, err := ledger.AmountFromBig(new(big.Int).Mul(value.Big(), big.NewInt(int64(input.Quantity))))
	if err != nil || cost.Big().Cmp(big.NewInt(db.MaxMoneyMilli)) > 0 {
		return ErrInvalid
	}
	return purchaseTickets(ctx, tx, user, at, id, cost, input.Quantity, wallet, wallet == 0, p)
}

func purchaseTickets(ctx context.Context, tx *sql.Tx, user, at int64, id string, cost ledger.Amount, quantity, wallet int, gameFirst bool, p *profile) error {
	general, err := ledger.UserAssetAccount(ctx, tx, user, ledger.General)
	if err != nil {
		return err
	}
	game, err := ledger.UserAssetAccount(ctx, tx, user, ledger.Game)
	if err != nil {
		return err
	}
	availableGame := game.Balance
	if !gameFirst {
		availableGame = ledger.Amount{}
	}
	payment, err := ledger.SplitGamePayment(cost, general.Balance, availableGame)
	if errors.Is(err, ledger.ErrInsufficientBalance) {
		return ErrCredits
	}
	if err != nil {
		return err
	}
	generalExternal, err := ledger.CodedAssetAccount(ctx, tx, "external", ledger.General)
	if err != nil {
		return err
	}
	gameExternal, err := ledger.CodedAssetAccount(ctx, tx, "external", ledger.Game)
	if err != nil {
		return err
	}
	plan, err := ledger.NewGachaTicketPurchase(ledger.Meta{OperationID: id, ActorUserID: user, CreatedAt: at}, ledger.AccountPair{General: general.ID, Game: game.ID},
		ledger.AccountPair{General: generalExternal.ID, Game: gameExternal.ID}, payment)
	if err != nil {
		return err
	}
	if _, err = ledger.Apply(ctx, tx, plan); err != nil {
		return err
	}
	p.Balance[wallet] += int64(quantity)
	return nil
}
func claimed(ctx context.Context, tx *sql.Tx, user int64, kind, id string) (bool, error) {
	var yes bool
	err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM gacha_claims WHERE user_id=? AND kind=? AND target=?)", user, kind, id).Scan(&yes)
	return yes, err
}
func redeem(ctx context.Context, tx *sql.Tx, user, at int64, product string, shop ShopSettings, p *profile) error {
	spend := [4]int64{}
	wallet, quantity, cap := 0, int64(0), shop.PermanentCap
	switch product {
	case "ticket-n":
		spend[0] = shop.NTicket
		quantity = 1
	case "ticket-r":
		spend[1] = shop.RTicket
		quantity = 1
	case "ticket-sr":
		spend[2] = shop.SRTicket
		wallet = 1
		quantity = 1
		cap = shop.LimitedCap
	case "ticket-ssr":
		spend[3] = shop.SSRTickets
		wallet = 1
		quantity = 2
		cap = shop.LimitedCap
	default:
		card, ok := cardsByID[product]
		if !ok || card.Source == "permanent" && card.Rarity != SSR {
			return ErrInvalid
		}
		owns, err := readOwned(ctx, tx, user)
		if err != nil {
			return err
		}
		if _, ok = owns[product]; ok {
			return ErrOwned
		}
		used, err := claimed(ctx, tx, user, "card", product)
		if err != nil {
			return err
		}
		if used {
			return ErrOwned
		}
		if card.Source == "shop" {
			spend[0] = shop.ChibiN
			spend[1] = shop.ChibiR
		} else if card.Source == "permanent" {
			spend[3] = shop.SSRCard
		} else {
			open := false
			err := tx.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM gacha_pools, json_each(config,'$.featured') card
 WHERE json_extract(config,'$.published')=1 AND json_extract(config,'$.paused')=0
 AND json_extract(config,'$.starts_at')<=? AND json_extract(config,'$.ends_at')>?
 AND card.value=?)`, at, at, product).Scan(&open)
			if err != nil {
				return err
			}
			if !open {
				return ErrClosed
			}
			if card.Rarity == SR {
				spend[2] = shop.SRCard
			} else if card.Rarity == SP {
				spend[3] = shop.SPCard
			} else if card.Rarity.Tier() == SSR {
				spend[3] = shop.SSRCard
			} else {
				return ErrInvalid
			}
		}
	}
	if quantity > 0 && (quantity > cap || p.Used[wallet] > cap-quantity) {
		return ErrMonthly
	}
	for i, n := range spend {
		if p.Balance[i+2] < n {
			return ErrFragments
		}
	}
	for i, n := range spend {
		p.Balance[i+2] -= n
	}
	if quantity > 0 {
		p.Balance[wallet] += quantity
		p.Used[wallet] += quantity
		return nil
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO gacha_owned(user_id,card_id,obtained_at,source) VALUES(?,?,?,'shop')", user, product, at); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO gacha_claims(user_id,kind,target,created_at) VALUES(?,'card',?,?)", user, product, at)
	return err
}
func claim(ctx context.Context, tx *sql.Tx, user, at int64, id, themeID string, p *profile) error {
	var theme *Theme
	for i := range catalog.Themes {
		if catalog.Themes[i].ID == themeID {
			theme = &catalog.Themes[i]
			break
		}
	}
	if theme == nil {
		return ErrInvalid
	}
	used, err := claimed(ctx, tx, user, "theme", themeID)
	if err != nil {
		return err
	}
	if used {
		return ErrClaimed
	}
	owned, err := readOwned(ctx, tx, user)
	if err != nil {
		return err
	}
	for _, card := range catalog.Cards {
		if card.Theme == themeID {
			if _, ok := owned[card.ID]; !ok {
				return ErrTheme
			}
		}
	}
	wallet := 0
	if theme.Source != "permanent" {
		wallet = 1
	}
	p.Balance[wallet]++
	general, err := ledger.UserAccount(ctx, tx, user)
	if err != nil {
		return err
	}
	game, err := ledger.UserAssetAccount(ctx, tx, user, ledger.Game)
	if err != nil {
		return err
	}
	asset := ledger.General
	if theme.Source == "permanent" {
		asset = ledger.Game
	}
	external, err := ledger.CodedAssetAccount(ctx, tx, "external", asset)
	if err != nil {
		return err
	}
	plan, err := ledger.NewGachaThemeReward(ledger.Meta{OperationID: id, ActorUserID: user, CreatedAt: at},
		ledger.AccountPair{General: general.ID, Game: game.ID}, external.ID, asset)
	if err != nil {
		return err
	}
	if _, err = ledger.Apply(ctx, tx, plan); err != nil {
		return err
	}
	if err = authz.PromoteDonationLevelTx(ctx, tx, user, at); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO gacha_claims(user_id,kind,target,created_at) VALUES(?,'theme',?,?)", user, themeID, at)
	return err
}

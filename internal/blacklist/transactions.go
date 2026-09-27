// Package blacklist owns the immutable first event and append-only notes of
// an active registration block. Callers authorize and retire affected users.
package blacklist

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrInvalid = errors.New("blacklist: invalid event")

type ActorKind string

const (
	Admin          ActorKind = "admin"
	Steward6       ActorKind = "steward6"
	Automatic      ActorKind = "automatic"
	PenaltyEvasion           = "deletion_penalty_evasion"
	DebtEvasion              = "deletion_debt_evasion"
)

type Event struct {
	DiscordID    string
	OperationKey string
	ActorKind    ActorKind
	ActorUserID  *int64
	ReasonCodes  []string
	Note         string
	At           int64
}

func ValidDiscordID(value string) bool {
	id, err := strconv.ParseUint(value, 10, 64)
	return err == nil && id > 0 && strconv.FormatUint(id, 10) == value
}

func NormalizeNote(value string) (string, error) {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 2000 || strings.TrimSpace(value) == "" {
		return "", ErrInvalid
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return "", ErrInvalid
		}
	}
	return value, nil
}

// AppendTx never replaces the original actor, note, or timestamp. A replay
// must match the full stored event; a reused operation key cannot hide a new
// or contradictory write. The block and event commit with the caller's TX.
func AppendTx(ctx context.Context, tx *sql.Tx, event Event) (string, error) {
	note, err := NormalizeNote(event.Note)
	if err != nil || ctx == nil || tx == nil || !ValidDiscordID(event.DiscordID) ||
		len(event.OperationKey) < 1 || len(event.OperationKey) > 128 ||
		event.At < 0 || event.At > 253402300799 ||
		(event.ActorKind != Admin && event.ActorKind != Steward6 && event.ActorKind != Automatic) ||
		(event.ActorUserID != nil && *event.ActorUserID <= 0) {
		return "", ErrInvalid
	}
	if event.ActorKind != Automatic && event.ActorUserID == nil {
		return "", ErrInvalid
	}
	codes := []string{}
	for _, code := range []string{PenaltyEvasion, DebtEvasion} {
		for _, candidate := range event.ReasonCodes {
			if candidate == code {
				codes = append(codes, code)
				break
			}
		}
	}
	for _, code := range event.ReasonCodes {
		if code != PenaltyEvasion && code != DebtEvasion {
			return "", ErrInvalid
		}
	}
	if (event.ActorKind == Automatic) != (len(codes) > 0) {
		return "", ErrInvalid
	}
	encoded, err := json.Marshal(codes)
	if err != nil {
		return "", err
	}
	var oldDiscord, oldKind, oldCodes, oldNote string
	var oldActor sql.NullInt64
	var oldAt int64
	err = tx.QueryRowContext(ctx, `SELECT discord_id,actor_kind,actor_user_id,reason_codes_json,safe_note,created_at FROM discord_blacklist_events WHERE operation_key=?`, event.OperationKey).Scan(&oldDiscord, &oldKind, &oldActor, &oldCodes, &oldNote, &oldAt)
	if err == nil {
		actorMatches := !oldActor.Valid && event.ActorUserID == nil || oldActor.Valid && event.ActorUserID != nil && oldActor.Int64 == *event.ActorUserID
		if oldDiscord != event.DiscordID || oldKind != string(event.ActorKind) || !actorMatches || oldCodes != string(encoded) || oldNote != note || oldAt != event.At {
			return "", ErrInvalid
		}
		return "appended", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO discord_blacklist(discord_id,reason,created_at) VALUES(?,?,?) ON CONFLICT(discord_id) DO NOTHING`, event.DiscordID, note, event.At)
	if err != nil {
		return "", err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	action := "appended"
	if n == 1 {
		action = "added"
		if _, err := tx.ExecContext(ctx, `INSERT INTO discord_blacklist_origins(discord_id,first_actor_kind,first_actor_user_id) VALUES(?,?,?)`, event.DiscordID, event.ActorKind, event.ActorUserID); err != nil {
			return "", err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO discord_blacklist_events(discord_id,operation_key,actor_kind,actor_user_id,reason_codes_json,safe_note,created_at) VALUES(?,?,?,?,?,?,?)`, event.DiscordID, event.OperationKey, event.ActorKind, event.ActorUserID, string(encoded), note, event.At)
	return action, err
}

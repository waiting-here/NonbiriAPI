package randomness

import (
	"context"
	"database/sql"
)

// Insert joins the game transaction after its parent resource exists. Exact
// foreign keys ensure this private child cannot outlive retained game data.
func Insert(ctx context.Context, tx *sql.Tx, secret *Secret) error {
	if secret == nil {
		return nil
	}
	body, err := secret.EncodePrivate()
	if err != nil {
		return err
	}
	var fishing, linklink, rps, duel, blackjack any
	switch secret.game {
	case "fishing":
		fishing = secret.resource
	case "linklink":
		linklink = secret.resource
	case "rps":
		rps = secret.resource
	case "bidding", "likes":
		duel = secret.resource
	case "blackjack":
		blackjack = secret.resource
	default:
		return ErrInvalid
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO game_random_proofs(resource_id,game_key,fishing_id,linklink_id,rps_id,duel_id,blackjack_id,private_json) VALUES(?,?,?,?,?,?,?,?)`, secret.resource, secret.game, fishing, linklink, rps, duel, blackjack, string(body))
	return err
}

// Load returns nil only for a retained game created before proof support.
func Load(ctx context.Context, tx *sql.Tx, game, resource string) (*Secret, error) {
	var body string
	err := tx.QueryRowContext(ctx, `SELECT private_json FROM game_random_proofs WHERE game_key=? AND resource_id=?`, game, resource).Scan(&body)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s, err := DecodePrivate([]byte(body))
	if err != nil || s.game != game || s.resource != resource {
		return nil, ErrInvalid
	}
	return s, nil
}

// Save never creates a missing commitment or changes its immutable identity.
func Save(ctx context.Context, tx *sql.Tx, secret *Secret) error {
	if secret == nil {
		return nil
	}
	body, err := secret.EncodePrivate()
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE game_random_proofs SET private_json=? WHERE resource_id=? AND game_key=?`, string(body), secret.resource, secret.game)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return ErrInvalid
	}
	return nil
}

func PublicTx(ctx context.Context, tx *sql.Tx, game, resource string, terminal bool) (*Proof, error) {
	if !terminal {
		// Read only the five public fields. Polling an active game neither loads
		// its private seed into an HTTP DTO nor replays its growing transcript.
		p := Proof{}
		err := tx.QueryRowContext(ctx, `SELECT json_extract(private_json,'$.algorithm'),game_key,resource_id,json_extract(private_json,'$.rules'),json_extract(private_json,'$.commitment') FROM game_random_proofs WHERE game_key=? AND resource_id=?`, game, resource).Scan(&p.Algorithm, &p.Game, &p.ResourceID, &p.Rules, &p.Commitment)
		if err == sql.ErrNoRows {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return &p, nil
	}
	s, err := Load(ctx, tx, game, resource)
	if err != nil || s == nil {
		return nil, err
	}
	proof := s.Public(terminal)
	return &proof, nil
}

// MoveToSummary transfers the foreign key before an older game's active row
// is deleted. Both the summary and this transfer use its settlement transaction.
func MoveToSummary(ctx context.Context, tx *sql.Tx, game, resource string) error {
	var query string
	switch game {
	case "linklink":
		query = `UPDATE game_random_proofs SET linklink_id=NULL,linklink_summary_id=resource_id WHERE game_key='linklink' AND resource_id=?`
	case "rps":
		query = `UPDATE game_random_proofs SET rps_id=NULL,rps_summary_id=resource_id WHERE game_key='rps' AND resource_id=?`
	default:
		return ErrInvalid
	}
	_, err := tx.ExecContext(ctx, query, resource)
	return err
}

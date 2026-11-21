package whalehaven

import (
	"database/sql"
	"errors"
	"strings"
)

type houseView struct {
	House  neighbor     `json:"house"`
	Guests []houseGuest `json:"guests"`
}

type houseGuest struct {
	Name   string `json:"whaleName"`
	Outfit string `json:"whaleOutfit"`
	Stage  string `json:"stage"`
	Mine   bool   `json:"mine"`
}

func (w *world) neighborHouse(viewer int64, door string) (houseView, error) {
	house, err := w.scanPeer(w.tx.QueryRowContext(w.ctx, peerSelect+" AND h.door=? AND h.user_id<>?", viewer, w.now/1000, strings.TrimSpace(door), viewer))
	if errors.Is(err, sql.ErrNoRows) {
		return houseView{}, errUnavailable
	}
	if err != nil {
		return houseView{}, err
	}
	host, err := w.load(house.account)
	if err != nil {
		return houseView{}, err
	}
	if err = w.settle(host); err != nil {
		return houseView{}, err
	}
	house.project(host)

	result := houseView{House: house, Guests: make([]houseGuest, 0, 2)}
	// The query admits visible guests at this door. The settled visits decide
	// who is still present without persisting any progress in this read.
	rows, err := w.tx.QueryContext(w.ctx, `SELECT v.guest_id, h.whale_name, json_extract(h.snapshot,'$.whale.outfit')
 FROM whale_haven_visits v
 JOIN whale_haven_profiles h ON h.user_id=v.guest_id
 JOIN users u ON u.id=h.user_id
 WHERE v.host_id=? AND v.started_at<=? AND h.is_open=1 AND `+visiblePeer+` ORDER BY v.guest_id LIMIT 2`, host.id, w.now, w.now/1000)
	if err != nil {
		return houseView{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var guest houseGuest
		if err = rows.Scan(&id, &guest.Name, &guest.Outfit); err != nil {
			return houseView{}, err
		}
		visit := w.visits[id]
		if visit == nil || visit.ended {
			continue
		}
		guest.Stage = "eating"
		if visit.working {
			guest.Stage = "working"
		}
		guest.Mine = id == viewer
		result.Guests = append(result.Guests, guest)
	}
	return result, rows.Err()
}

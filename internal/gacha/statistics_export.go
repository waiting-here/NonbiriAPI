package gacha

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// ExportStatistics streams only aggregate facts from one authorized snapshot.
// It does not accept table filters or pagination, and never reads membership IDs.
func (s *Service) ExportStatistics(ctx context.Context, actor int64, id, format string, writer io.Writer) error {
	if format != "json" && format != "csv" {
		return ErrInvalid
	}
	tx, permanent, err := s.statisticsTx(ctx, actor)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	pool, roster, err := statisticsPoolTx(ctx, tx, permanent, id)
	if err != nil {
		return err
	}
	rarities, err := rarityStatisticsTx(ctx, tx, id, roster)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, statisticsCardsSQL+`SELECT `+statisticsCardColumns+` FROM cards ORDER BY draw_count DESC,card_id`, statisticsCardArgs(id, roster)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var csvWriter *csv.Writer
	if format == "csv" {
		csvWriter = csv.NewWriter(writer)
		if err = csvWriter.Write([]string{"pool_id", "pool_name", "total_draws", "pool_participant_count", "card_id", "name", "name_en", "rarity", "count", "participant_count", "first_acquisition_count"}); err != nil {
			return err
		}
	} else {
		header, err := json.Marshal(struct {
			Pool         PoolStatistics     `json:"pool"`
			RarityTotals []RarityStatistics `json:"rarity_totals"`
		}{pool, rarities})
		if err != nil {
			return err
		}
		if _, err = writer.Write(append(header[:len(header)-1], []byte(",\"cards\":[")...)); err != nil {
			return err
		}
	}
	first := true
	for rows.Next() {
		item, err := scanCardStatistics(rows)
		if err != nil {
			return err
		}
		if csvWriter != nil {
			rarity := ""
			if item.Rarity != nil {
				rarity = string(*item.Rarity)
			}
			record := []string{pool.ID, pool.Name, pool.TotalDraws, pool.ParticipantCount, item.CardID, item.Name, item.NameEN, rarity, item.Count, item.ParticipantCount, item.FirstAcquisitionCount}
			for index, value := range record {
				record[index] = statisticsCSVCell(value)
			}
			if err = csvWriter.Write(record); err != nil {
				return err
			}
		} else {
			if !first {
				if _, err = io.WriteString(writer, ","); err != nil {
					return err
				}
			}
			first = false
			body, err := json.Marshal(item)
			if err != nil {
				return err
			}
			if _, err = writer.Write(body); err != nil {
				return err
			}
		}
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if csvWriter != nil {
		csvWriter.Flush()
		if err = csvWriter.Error(); err != nil {
			return err
		}
	} else {
		if _, err = io.WriteString(writer, "]}\n"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func statisticsCSVCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	if strings.HasPrefix(value, "\t") || strings.HasPrefix(value, "\r") || strings.HasPrefix(value, "\n") {
		return "'" + value
	}
	return value
}

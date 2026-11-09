package accounting

import (
	"fmt"
	"math/big"
	"sort"
	"strings"

	"go.etcd.io/bbolt"
)

func dayMatches(day string, q Query) bool {
	return (q.From == "" || day >= q.From) && (q.To == "" || day <= q.To)
}

func pageBounds(length int, q Query) (int, int) {
	start := max(0, min(q.Offset, length))
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 200)
	return start, min(start+limit, length)
}

func (s *Store) Accounts(q Query) ([]Account, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]Account, 0)
	if s.db == nil {
		return items, 0, fmt.Errorf("usage ledger unavailable")
	}
	errView := s.db.View(func(tx *bbolt.Tx) error {
		filtered := q.From != "" || q.To != "" || q.Model != ""
		totals := make(map[string]Totals)
		if filtered {
			if errEach := tx.Bucket([]byte("daily")).ForEach(func(_, raw []byte) error {
				var row DailyTotal
				if errDecode := decode(raw, &row); errDecode != nil {
					return errDecode
				}
				if !dayMatches(row.Day, q) || (q.Model != "" && q.Model != row.Model) {
					return nil
				}
				total := totals[row.AccountID]
				if errTotal := total.merge(row.Totals); errTotal != nil {
					return errTotal
				}
				totals[row.AccountID] = total
				return nil
			}); errEach != nil {
				return errEach
			}
		}
		return tx.Bucket([]byte("accounts")).ForEach(func(_, raw []byte) error {
			var account Account
			if errDecode := decode(raw, &account); errDecode != nil {
				return errDecode
			}
			if q.Provider != "" && account.Provider != q.Provider {
				return nil
			}
			if filtered {
				total, ok := totals[account.ID]
				if !ok {
					return nil
				}
				account.Totals = total
			}
			items = append(items, account)
			return nil
		})
	})
	if errView != nil {
		return nil, 0, errView
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		switch q.Sort {
		case "output_usd", "total_usd":
			x, y := a.Totals.OutputUSD, b.Totals.OutputUSD
			if q.Sort == "total_usd" {
				x, y = a.Totals.TotalUSD, b.Totals.TotalUSD
			}
			xr, okX := new(big.Rat).SetString(x)
			yr, okY := new(big.Rat).SetString(y)
			if okX && okY && xr.Cmp(yr) != 0 {
				return xr.Cmp(yr) > 0
			}
		case "output_tokens":
			if a.Totals.OutputTokens != b.Totals.OutputTokens {
				return a.Totals.OutputTokens > b.Totals.OutputTokens
			}
		default:
			if !a.LastSeen.Equal(b.LastSeen) {
				return a.LastSeen.After(b.LastSeen)
			}
		}
		return a.ID < b.ID
	})
	total := len(items)
	start, end := pageBounds(total, q)
	return items[start:end], total, nil
}

type AccountDetail struct {
	Account Account           `json:"account"`
	Daily   []DailyTotal      `json:"daily"`
	Models  map[string]Totals `json:"models"`
}

var ErrAccountNotFound = fmt.Errorf("usage account not found")

func (s *Store) Account(id string, q Query) (AccountDetail, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := AccountDetail{Daily: make([]DailyTotal, 0), Models: make(map[string]Totals)}
	if s.db == nil {
		return result, fmt.Errorf("usage ledger unavailable")
	}
	errView := s.db.View(func(tx *bbolt.Tx) error {
		data := tx.Bucket([]byte("accounts")).Get([]byte(id))
		if data == nil {
			return ErrAccountNotFound
		}
		if errDecode := decode(data, &result.Account); errDecode != nil {
			return errDecode
		}
		filtered := Totals{OutputUSD: "0", TotalUSD: "0"}
		cursor := tx.Bucket([]byte("daily")).Cursor()
		prefix := id + "\x00"
		for key, raw := cursor.Seek([]byte(prefix)); key != nil && strings.HasPrefix(string(key), prefix); key, raw = cursor.Next() {
			var row DailyTotal
			if errDecode := decode(raw, &row); errDecode != nil {
				return errDecode
			}
			if !dayMatches(row.Day, q) || (q.Model != "" && row.Model != q.Model) {
				continue
			}
			result.Daily = append(result.Daily, row)
			total := result.Models[row.Model]
			if errTotal := total.merge(row.Totals); errTotal != nil {
				return errTotal
			}
			result.Models[row.Model] = total
			if errTotal := filtered.merge(row.Totals); errTotal != nil {
				return errTotal
			}
		}
		if q.From != "" || q.To != "" || q.Model != "" {
			result.Account.Totals = filtered
		}
		return nil
	})
	return result, errView
}

func (s *Store) Events(accountID string, q Query) ([]Event, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]Event, 0)
	if s.db == nil {
		return items, 0, fmt.Errorf("usage ledger unavailable")
	}
	total := 0
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 200)
	errView := s.db.View(func(tx *bbolt.Tx) error {
		if tx.Bucket([]byte("accounts")).Get([]byte(accountID)) == nil {
			return ErrAccountNotFound
		}
		prefix := accountID + "\x00"
		cursor := tx.Bucket([]byte("account-events")).Cursor()
		key, id := cursor.Seek([]byte(accountID + "\x01"))
		if key == nil {
			key, id = cursor.Last()
		} else {
			key, id = cursor.Prev()
		}
		for ; key != nil && strings.HasPrefix(string(key), prefix); key, id = cursor.Prev() {
			var event Event
			if errDecode := decode(tx.Bucket([]byte("events")).Get(id), &event); errDecode != nil {
				return errDecode
			}
			day := event.RequestedAt.Format("2006-01-02")
			if !dayMatches(day, q) || (q.Model != "" && q.Model != event.PricingModel) {
				continue
			}
			if total >= q.Offset && len(items) < limit {
				items = append(items, event)
			}
			total++
		}
		return nil
	})
	return items, total, errView
}

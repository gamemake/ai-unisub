package database

import (
	"ai-unisub/internal/proxy"
	"time"
)

func (s *SQLiteDatabase) SaveProxyStats(values []proxy.Bucket) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, b := range values {
		_, err = tx.Exec(`INSERT INTO proxy_stats(address, application, start_at, source, requests, failures) VALUES(?,?,?,?,?,?) ON CONFLICT(address,application,start_at,source) DO UPDATE SET requests=excluded.requests, failures=excluded.failures`, b.Address, b.Application, b.StartAt.UTC().Unix(), b.Source, b.Requests, b.Failures)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *SQLiteDatabase) ListProxyStats(address, app string, from, to time.Time) ([]proxy.Bucket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT start_at,SUM(requests),SUM(failures) FROM proxy_stats WHERE address=? AND application=? AND start_at>=? AND start_at<? GROUP BY start_at ORDER BY start_at`, address, app, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []proxy.Bucket{}
	for rows.Next() {
		b := proxy.Bucket{Address: address, Application: app}
		var at int64
		if err := rows.Scan(&at, &b.Requests, &b.Failures); err != nil {
			return nil, err
		}
		b.StartAt = time.Unix(at, 0).UTC()
		out = append(out, b)
	}
	return out, rows.Err()
}

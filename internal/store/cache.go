// Package store 本地缓存（modernc.org/sqlite，纯 Go，无 CGO，便于静态分发）。
package store

import (
	"database/sql"
	"strings"
	"sync"
	"time"

	"gobot/internal/config"

	_ "modernc.org/sqlite"
)

var (
	db   *sql.DB
	once sync.Once
	oerr error
)

func open() (*sql.DB, error) {
	once.Do(func() {
		d, err := sql.Open("sqlite", config.DBPath())
		if err != nil {
			oerr = err
			return
		}
		if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS users (
			sec_uid TEXT PRIMARY KEY, uid TEXT NOT NULL DEFAULT '',
			nickname TEXT NOT NULL DEFAULT '', avatar TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL DEFAULT 0)`); err != nil {
			oerr = err
			return
		}
		db = d
	})
	return db, oerr
}

// CachedUser 缓存的用户资料。
type CachedUser struct {
	SecUID, UID, Nickname, Avatar string
	UpdatedAt                     int64
}

// GetCachedUsers 批量取缓存。
func GetCachedUsers(secUids []string) map[string]CachedUser {
	out := make(map[string]CachedUser)
	if len(secUids) == 0 {
		return out
	}
	d, err := open()
	if err != nil || d == nil {
		return out
	}
	ph := strings.Repeat("?,", len(secUids))
	ph = ph[:len(ph)-1]
	args := make([]any, len(secUids))
	for i, s := range secUids {
		args[i] = s
	}
	rows, err := d.Query(`SELECT sec_uid, uid, nickname, avatar, updated_at FROM users WHERE sec_uid IN (`+ph+`)`, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var u CachedUser
		if rows.Scan(&u.SecUID, &u.UID, &u.Nickname, &u.Avatar, &u.UpdatedAt) == nil {
			out[u.SecUID] = u
		}
	}
	return out
}

// PutUsers 批量写入/更新。
func PutUsers(users []CachedUser) {
	if len(users) == 0 {
		return
	}
	d, err := open()
	if err != nil || d == nil {
		return
	}
	now := time.Now().Unix()
	stmt, err := d.Prepare(`INSERT INTO users (sec_uid, uid, nickname, avatar, updated_at) VALUES (?,?,?,?,?)
		ON CONFLICT(sec_uid) DO UPDATE SET uid=excluded.uid, nickname=excluded.nickname,
		avatar=excluded.avatar, updated_at=excluded.updated_at`)
	if err != nil {
		return
	}
	defer stmt.Close()
	for _, u := range users {
		_, _ = stmt.Exec(u.SecUID, u.UID, u.Nickname, u.Avatar, now)
	}
}

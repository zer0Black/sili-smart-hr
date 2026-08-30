package dberr

import (
	"errors"
	"fmt"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// TestUniqueViolation 按错误形态表驱动断言：主路径 errors.Is(gorm.ErrDuplicatedKey)
//（生产已开 TranslateError，三库统一翻译），防御兜底覆盖结构化驱动错误
//（SQLite 2067 / MySQL 1062 / PG 23505，TranslateError 关闭的测试直连路径），其余不命中。
func TestUniqueViolation(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"gorm.ErrDuplicatedKey 主路径", gorm.ErrDuplicatedKey, true},
		{"wrap 后 ErrDuplicatedKey", fmt.Errorf("create session feature: %w", gorm.ErrDuplicatedKey), true},
		{"MySQL 1062 结构化兜底", &mysql.MySQLError{Number: 1062}, true},
		{"MySQL 非 1062 结构化不命中", &mysql.MySQLError{Number: 1045}, false},
		{"PG 23505 结构化兜底", &pgconn.PgError{Code: "23505"}, true},
		{"PG 非 23505 结构化不命中", &pgconn.PgError{Code: "23503"}, false},
		{"wrap 后结构化错误链", fmt.Errorf("create session feature: %w", &mysql.MySQLError{Number: 1062}), true},
		{"SQLite BUSY 不命中", errors.New("database is locked (5) (SQLITE_BUSY)"), false},
		{"普通错误串不命中（错误串兜底已随翻译层落地移除）", errors.New("UNIQUE constraint failed: session_features.session_key"), false},
	}
	for _, tc := range cases {
		if got := UniqueViolation(tc.err); got != tc.want {
			t.Errorf("%s: want %v, got %v (err=%v)", tc.name, tc.want, got, tc.err)
		}
	}
}

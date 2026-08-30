// Package dberr 承载跨层共享的数据库错误判定工具。
// 上提原因：model 与 repository 都需要（迁移 seed 撞键收敛、Save 冲突收敛），
// 留在任一侧都会形成 model→repository 或反向的 import 环。
package dberr

import (
	"errors"

	gosqlite "github.com/glebarez/go-sqlite"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// UniqueViolation 判定唯一索引冲突：生产 gorm.Config 已开 TranslateError，
// 三库（glebarez/sqlite、MySQL、PG）驱动冲突统一翻译为 gorm.ErrDuplicatedKey，
// errors.Is 单点判定即可；防御性保留旧驱动错误码兜底（翻译层关闭的测试直连路径）。
func UniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	// 防御兜底：TranslateError 关闭时的原生错误形态（SQLite 2067 / MySQL 1062 / PG 23505）。
	var sqliteErr *gosqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == 2067 { // SQLITE_CONSTRAINT_UNIQUE
		return true
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 { // ER_DUP_ENTRY
		return true
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" // unique_violation
}

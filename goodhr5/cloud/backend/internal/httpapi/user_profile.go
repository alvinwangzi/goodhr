// 本文件负责封装用户个人信息（昵称、密码哈希）的读写能力。
package httpapi

import (
	"context"
	"database/sql"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// UserProfileStore 定义用户昵称和密码哈希的读写能力。
type UserProfileStore interface {
	// GetDisplayName 读取指定邮箱用户的昵称。
	GetDisplayName(email string) (string, error)
	// UpdateDisplayName 更新指定邮箱用户的昵称。
	UpdateDisplayName(email string, displayName string) error
	// GetPasswordHash 读取指定邮箱用户的密码哈希，未设置时返回空字符串。
	GetPasswordHash(email string) (string, error)
	// UpdatePasswordHash 更新指定邮箱用户的密码哈希。
	UpdatePasswordHash(email string, hash string) error
}

// PostgresUserProfileStore 使用 PostgreSQL 实现用户资料读写。
type PostgresUserProfileStore struct {
	db *sql.DB
}

// NewPostgresUserProfileStore 创建基于 PostgreSQL 的用户资料存储。
func NewPostgresUserProfileStore(db *sql.DB) *PostgresUserProfileStore {
	return &PostgresUserProfileStore{db: db}
}

// GetDisplayName 读取指定邮箱用户的昵称。
func (s *PostgresUserProfileStore) GetDisplayName(email string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var displayName string
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(display_name, '') FROM users WHERE email = $1`, email,
	).Scan(&displayName)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return displayName, nil
}

// UpdateDisplayName 更新指定邮箱用户的昵称。
func (s *PostgresUserProfileStore) UpdateDisplayName(email string, displayName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET display_name = $1 WHERE email = $2`, displayName, email,
	)
	return err
}

// GetPasswordHash 读取指定邮箱用户的密码哈希，未设置时返回空字符串。
func (s *PostgresUserProfileStore) GetPasswordHash(email string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var hash sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT password_hash FROM users WHERE email = $1`, email,
	).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return hash.String, nil
}

// UpdatePasswordHash 更新指定邮箱用户的密码哈希。
func (s *PostgresUserProfileStore) UpdatePasswordHash(email string, hash string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash = $1 WHERE email = $2`, hash, email,
	)
	return err
}

// MemoryUserProfileStore 提供无数据库环境下的内存实现，仅用于开发测试。
type MemoryUserProfileStore struct {
	displayNames map[string]string
	passwordHash map[string]string
}

// NewMemoryUserProfileStore 创建内存用户资料存储。
func NewMemoryUserProfileStore() *MemoryUserProfileStore {
	return &MemoryUserProfileStore{
		displayNames: make(map[string]string),
		passwordHash: make(map[string]string),
	}
}

// GetDisplayName 在内存中读取用户昵称。
func (s *MemoryUserProfileStore) GetDisplayName(email string) (string, error) {
	return s.displayNames[email], nil
}

// UpdateDisplayName 在内存中更新用户昵称。
func (s *MemoryUserProfileStore) UpdateDisplayName(email string, displayName string) error {
	s.displayNames[email] = displayName
	return nil
}

// GetPasswordHash 在内存中读取密码哈希。
func (s *MemoryUserProfileStore) GetPasswordHash(email string) (string, error) {
	return s.passwordHash[email], nil
}

// UpdatePasswordHash 在内存中更新密码哈希。
func (s *MemoryUserProfileStore) UpdatePasswordHash(email string, hash string) error {
	s.passwordHash[email] = hash
	return nil
}

// hashPassword 使用 bcrypt 对明文密码进行哈希。
func hashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// checkPassword 校验明文密码是否与 bcrypt 哈希匹配。
func checkPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

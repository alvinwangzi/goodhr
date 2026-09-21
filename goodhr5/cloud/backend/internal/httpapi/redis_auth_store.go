package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisAuthStore struct {
	client *redis.Client
}

func NewRedisAuthStore(addr string, password string, db int) *RedisAuthStore {
	return &RedisAuthStore{
		client: redis.NewClient(&redis.Options{
			Addr:     addr,
			Password: password,
			DB:       db,
		}),
	}
}

// Ping 检查 Redis 客户端是否能正常连接。
func (s *RedisAuthStore) Ping(ctx context.Context) error {
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.client.Ping(pingCtx).Err(); err != nil {
		return fmt.Errorf("Redis 连接失败: %w", err)
	}
	return nil
}

func (s *RedisAuthStore) SaveLoginCode(email string, code string, ttl time.Duration) error {
	return s.client.Set(context.Background(), loginCodeKey(email), code, ttl).Err()
}

func (s *RedisAuthStore) ConsumeLoginCode(email string, code string) (bool, error) {
	ctx := context.Background()
	key := loginCodeKey(email)
	saved, err := s.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if saved != code {
		return false, nil
	}
	if err := s.client.Del(ctx, key).Err(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *RedisAuthStore) SaveSession(token string, session Session, ttl time.Duration) error {
	ctx := context.Background()
	session.ExpiresAt = time.Now().Add(ttl)
	body, err := json.Marshal(session)
	if err != nil {
		return err
	}
	if err := s.client.Set(ctx, sessionKey(token), body, ttl).Err(); err != nil {
		return err
	}
	return nil
}

func (s *RedisAuthStore) GetSession(token string) (Session, error) {
	ctx := context.Background()
	body, err := s.client.Get(ctx, sessionKey(token)).Bytes()
	if err == redis.Nil {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}

	var session Session
	if err := json.Unmarshal(body, &session); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *RedisAuthStore) GetSessionUnsafe(token string) (Session, error) {
	body, err := s.client.Get(context.Background(), sessionKey(token)).Bytes()
	if err == redis.Nil {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	var session Session
	if err := json.Unmarshal(body, &session); err != nil {
		return Session{}, err
	}
	return session, nil
}

func loginCodeKey(email string) string {
	return "login_code:" + email
}

func sessionKey(token string) string {
	return "session:" + token
}

func currentSessionKey(email string) string {
	return "session_current:" + email
}

func passwordHashKey(email string) string {
	return "password_hash:" + email
}

func loginFailCountKey(email string) string {
	return "login_fail_count:" + email
}

func loginLockUntilKey(email string) string {
	return "login_lock_until:" + email
}

// GetUserPasswordHash 获取用户密码哈希。
func (s *RedisAuthStore) GetUserPasswordHash(email string) (string, error) {
	hash, err := s.client.Get(context.Background(), passwordHashKey(email)).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return hash, nil
}

// SetUserPasswordHash 设置用户密码哈希。
func (s *RedisAuthStore) SetUserPasswordHash(email string, hash string) error {
	return s.client.Set(context.Background(), passwordHashKey(email), hash, 0).Err()
}

// GetLoginFailCount 获取密码登录失败次数。
func (s *RedisAuthStore) GetLoginFailCount(email string) (int, error) {
	count, err := s.client.Get(context.Background(), loginFailCountKey(email)).Int()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return count, nil
}

// SetLoginFailCount 设置密码登录失败次数。
func (s *RedisAuthStore) SetLoginFailCount(email string, count int, ttl time.Duration) error {
	return s.client.Set(context.Background(), loginFailCountKey(email), count, ttl).Err()
}

// GetLoginLockUntil 获取密码登录锁定截止时间。
func (s *RedisAuthStore) GetLoginLockUntil(email string) (time.Time, error) {
	val, err := s.client.Get(context.Background(), loginLockUntilKey(email)).Result()
	if err == redis.Nil {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	until, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return time.Time{}, err
	}
	return until, nil
}

// SetLoginLockUntil 设置密码登录锁定截止时间。
func (s *RedisAuthStore) SetLoginLockUntil(email string, until time.Time, ttl time.Duration) error {
	return s.client.Set(context.Background(), loginLockUntilKey(email), until.Format(time.RFC3339), ttl).Err()
}

// ClearLoginFailState 清除密码登录失败状态。
func (s *RedisAuthStore) ClearLoginFailState(email string) error {
	ctx := context.Background()
	if err := s.client.Del(ctx, loginFailCountKey(email)).Err(); err != nil {
		return err
	}
	return s.client.Del(ctx, loginLockUntilKey(email)).Err()
}

package httpapi

import (
	"errors"
	"sync"
	"time"
)

var ErrNotFound = errors.New("not found")

type AuthStore interface {
	SaveLoginCode(email string, code string, ttl time.Duration) error
	ConsumeLoginCode(email string, code string) (bool, error)
	SaveSession(token string, session Session, ttl time.Duration) error
	GetSession(token string) (Session, error)
	GetSessionUnsafe(token string) (Session, error)
	// PasswordStore 密码登录相关操作（可选实现）
	PasswordStore
}

// PasswordStore 定义密码登录相关的存储接口。
type PasswordStore interface {
	// GetUserPasswordHash 获取用户密码哈希，返回空字符串表示未设置密码
	GetUserPasswordHash(email string) (string, error)
	// SetUserPasswordHash 设置用户密码哈希
	SetUserPasswordHash(email string, hash string) error
	// GetLoginFailCount 获取密码登录失败次数
	GetLoginFailCount(email string) (int, error)
	// SetLoginFailCount 设置密码登录失败次数
	SetLoginFailCount(email string, count int, ttl time.Duration) error
	// GetLoginLockUntil 获取密码登录锁定截止时间，返回零值表示未锁定
	GetLoginLockUntil(email string) (time.Time, error)
	// SetLoginLockUntil 设置密码登录锁定截止时间
	SetLoginLockUntil(email string, until time.Time, ttl time.Duration) error
	// ClearLoginFailState 清除密码登录失败状态（验证码登录成功时调用）
	ClearLoginFailState(email string) error
}

type Session struct {
	Email     string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type MemoryAuthStore struct {
	mu              sync.Mutex
	codes           map[string]loginCode
	sessions        map[string]Session
	passwordHashes  map[string]string
	failCounts      map[string]int
	lockUntil       map[string]time.Time
	now             func() time.Time
}

type loginCode struct {
	Code      string
	ExpiresAt time.Time
}

func NewMemoryAuthStore() *MemoryAuthStore {
	return &MemoryAuthStore{
		codes:          make(map[string]loginCode),
		sessions:       make(map[string]Session),
		passwordHashes: make(map[string]string),
		failCounts:     make(map[string]int),
		lockUntil:      make(map[string]time.Time),
		now:            time.Now,
	}
}

func (s *MemoryAuthStore) SaveLoginCode(email string, code string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.codes[email] = loginCode{
		Code:      code,
		ExpiresAt: s.now().Add(ttl),
	}
	return nil
}

func (s *MemoryAuthStore) ConsumeLoginCode(email string, code string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	saved, ok := s.codes[email]
	if !ok {
		return false, nil
	}
	if s.now().After(saved.ExpiresAt) {
		delete(s.codes, email)
		return false, nil
	}
	if saved.Code != code {
		return false, nil
	}

	delete(s.codes, email)
	return true, nil
}

func (s *MemoryAuthStore) SaveSession(token string, session Session, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session.ExpiresAt = s.now().Add(ttl)
	s.sessions[token] = session
	return nil
}

func (s *MemoryAuthStore) GetSession(token string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[token]
	if !ok {
		return Session{}, ErrNotFound
	}
	if s.now().After(session.ExpiresAt) {
		delete(s.sessions, token)
		return Session{}, ErrNotFound
	}
	return session, nil
}

func (s *MemoryAuthStore) GetSessionUnsafe(token string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[token]
	if !ok {
		return Session{}, ErrNotFound
	}
	if s.now().After(session.ExpiresAt) {
		delete(s.sessions, token)
		return Session{}, ErrNotFound
	}
	return session, nil
}

// GetUserPasswordHash 获取用户密码哈希。
func (s *MemoryAuthStore) GetUserPasswordHash(email string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hash, ok := s.passwordHashes[email]
	if !ok {
		return "", nil
	}
	return hash, nil
}

// SetUserPasswordHash 设置用户密码哈希。
func (s *MemoryAuthStore) SetUserPasswordHash(email string, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.passwordHashes[email] = hash
	return nil
}

// GetLoginFailCount 获取密码登录失败次数。
func (s *MemoryAuthStore) GetLoginFailCount(email string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failCounts[email], nil
}

// SetLoginFailCount 设置密码登录失败次数。
func (s *MemoryAuthStore) SetLoginFailCount(email string, count int, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failCounts[email] = count
	return nil
}

// GetLoginLockUntil 获取密码登录锁定截止时间。
func (s *MemoryAuthStore) GetLoginLockUntil(email string) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	until, ok := s.lockUntil[email]
	if !ok {
		return time.Time{}, nil
	}
	return until, nil
}

// SetLoginLockUntil 设置密码登录锁定截止时间。
func (s *MemoryAuthStore) SetLoginLockUntil(email string, until time.Time, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lockUntil[email] = until
	return nil
}

// ClearLoginFailState 清除密码登录失败状态。
func (s *MemoryAuthStore) ClearLoginFailState(email string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failCounts, email)
	delete(s.lockUntil, email)
	return nil
}

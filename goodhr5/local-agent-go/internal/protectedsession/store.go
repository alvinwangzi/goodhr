// 本文件保存 HRPlus 后台登录的受保护会话，磁盘不写明文令牌，普通 JSON 和日志格式化也不输出令牌。
package protectedsession

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const envelopeHeader = "HRPLUS_SESSION_DPAPI_V1\n"

// Available 只有已接入系统保护的 Windows 才声明支持后台持久登录。
func Available() bool { return runtime.GOOS == "windows" }

// Session 是进程内已核对身份；保存不等于云端授权，启动后必须重新验证。
type Session struct {
	CloudBase string `json:"cloud_base"`
	UserEmail string `json:"user_email"`
	TenantID  string `json:"tenant_id"`
	MachineID string `json:"machine_id"`
	Token     string `json:"-"`
}

// String 防止普通日志格式化输出令牌。
func (s Session) String() string {
	return fmt.Sprintf("HRPlusSession{user=%s, device=%s, token=[hidden]}", s.UserEmail, s.MachineID)
}

// GoString 防止详细 Go 格式化绕过令牌隐藏。
func (s Session) GoString() string { return s.String() }

// Store 只持有固定文件路径和本地锁，不把解密内容缓存到磁盘。
type Store struct {
	mu   sync.Mutex
	path string
}

// New 为当前数据目录建立固定存储路径，不读取或创建凭证文件。
func New(directory string) *Store {
	return &Store{path: filepath.Join(directory, "protected-session.bin")}
}

// Save 先加密，再原子替换原会话，失败时不降级为明文文件。
func (s *Store) Save(value Session) error {
	if value.CloudBase == "" || value.UserEmail == "" || value.MachineID == "" || value.Token == "" {
		return errors.New("会话缺少已核对的登录事实")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	payload := struct {
		Session
		Credential string `json:"credential"`
	}{value, value.Token}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	defer clear(raw)
	cipher, err := protect(raw)
	if err != nil {
		return fmt.Errorf("受保护会话加密失败：%w", err)
	}
	if err = os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".protected-session-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(append([]byte(envelopeHeader), cipher...))
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(file.Name(), s.path); err != nil {
		return err
	}
	if err = os.Remove(s.path + ".revoked"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Load 解密当前用户的会话；损坏、错误格式或其他用户的文件不能当成已登录。
func (s *Store) Load() (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result Session
	if _, err := os.Stat(s.path + ".revoked"); err == nil {
		return result, os.ErrNotExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return result, err
	}
	if len(raw) > 1<<20 || !strings.HasPrefix(string(raw), envelopeHeader) {
		return result, errors.New("受保护会话格式不正确")
	}
	plain, err := unprotect(raw[len(envelopeHeader):])
	if err != nil {
		return result, fmt.Errorf("受保护会话无法解密：%w", err)
	}
	defer clear(plain)
	var payload struct {
		Session
		Credential string `json:"credential"`
	}
	if err = json.Unmarshal(plain, &payload); err != nil {
		return result, errors.New("受保护会话内容损坏")
	}
	result = payload.Session
	result.Token = payload.Credential
	if result.CloudBase == "" || result.UserEmail == "" || result.MachineID == "" || result.Token == "" {
		return Session{}, errors.New("受保护会话内容缺失")
	}
	return result, nil
}

// Clear 清除固定的凭证文件，未存在时也视为已经退出。
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(s.path+".revoked", []byte("revoked"), 0600); err != nil {
		return err
	}
	err := os.Remove(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

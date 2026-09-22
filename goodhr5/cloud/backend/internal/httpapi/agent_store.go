// 本文件负责定义云端 Agent 本地程序连接记录的数据模型和存储接口。
package httpapi

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const stableAgentMachineIDPrefix = "goodhr-device-v1-"

// AgentBindingConflictError 表示稳定设备已被另一个账号占用。
type AgentBindingConflictError struct {
	OwnerEmail string
}

// Error 返回包含已绑定完整邮箱和下一步处理方式的用户提示。
func (e *AgentBindingConflictError) Error() string {
	return fmt.Sprintf("这台电脑已经绑定到账号 %s，当前账号暂时不能再绑定。请退出后使用已绑定账号登录，或者请超级管理员先在用户管理里解绑。", e.OwnerEmail)
}

// AgentBinding 表示一个云端账号和一台本地程序的连接记录。
type AgentBinding struct {
	UserEmail    string
	MachineID    string
	AgentVersion string
	LocalPort    int
	PublicKey    string
	BindStatus   string
	LastSeenAt   time.Time
	CreatedAt    time.Time
}

// AgentStore 定义本地程序连接记录的持久化能力。
type AgentStore interface {
	SaveBinding(binding AgentBinding) (AgentBinding, error)
	CurrentBinding(userEmail string) (AgentBinding, error)
	HasActiveBinding(userEmail string, machineID string) (bool, error)
	DisableBindings(userEmail string) error
	ActiveBindingCount() (int, error)
}

// MemoryAgentStore 提供开发期使用的内存连接记录存储。
type MemoryAgentStore struct {
	mu       sync.Mutex
	bindings map[string]AgentBinding
	now      func() time.Time
}

// NewMemoryAgentStore 创建开发期内存连接记录存储。
func NewMemoryAgentStore() *MemoryAgentStore {
	return &MemoryAgentStore{
		bindings: make(map[string]AgentBinding),
		now:      time.Now,
	}
}

// SaveBinding 保存或更新当前用户的本地程序连接记录。
func (s *MemoryAgentStore) SaveBinding(binding AgentBinding) (AgentBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if isStableAgentMachineID(binding.MachineID) {
		for _, existing := range s.bindings {
			if existing.BindStatus == "active" && existing.MachineID == binding.MachineID &&
				!strings.EqualFold(existing.UserEmail, binding.UserEmail) {
				return AgentBinding{}, &AgentBindingConflictError{OwnerEmail: existing.UserEmail}
			}
		}
	}
	now := s.now()
	key := agentBindingKey(binding.UserEmail, binding.MachineID)
	if existing, ok := s.bindings[key]; ok {
		binding.CreatedAt = existing.CreatedAt
	}
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = now
	}
	binding.LastSeenAt = now
	if binding.BindStatus == "" {
		binding.BindStatus = "active"
	}

	s.bindings[key] = binding
	return binding, nil
}

// CurrentBinding 读取当前用户最近连接的一台本地机器。
func (s *MemoryAgentStore) CurrentBinding(userEmail string) (AgentBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var binding AgentBinding
	found := false
	for _, item := range s.bindings {
		if item.BindStatus != "active" || !strings.EqualFold(item.UserEmail, userEmail) {
			continue
		}
		if !found || item.LastSeenAt.After(binding.LastSeenAt) {
			binding = item
			found = true
		}
	}
	if !found {
		return AgentBinding{}, ErrNotFound
	}
	return binding, nil
}

// HasActiveBinding 判断指定账号与设备是否存在有效绑定。
func (s *MemoryAgentStore) HasActiveBinding(userEmail string, machineID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, ok := s.bindings[agentBindingKey(userEmail, machineID)]
	return ok && binding.BindStatus == "active", nil
}

// DisableBindings 清理当前用户所有本地程序连接记录。
func (s *MemoryAgentStore) DisableBindings(userEmail string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key, binding := range s.bindings {
		if !strings.EqualFold(binding.UserEmail, userEmail) || binding.BindStatus != "active" {
			continue
		}
		binding.BindStatus = "disabled"
		binding.LastSeenAt = s.now()
		s.bindings[key] = binding
	}
	return nil
}

// isStableAgentMachineID 判断机器码是否来自新版稳定硬件编号算法。
func isStableAgentMachineID(machineID string) bool {
	return strings.HasPrefix(strings.TrimSpace(machineID), stableAgentMachineIDPrefix)
}

// agentBindingKey 生成内存存储使用的账号和设备联合键。
func agentBindingKey(email string, machineID string) string {
	return strings.ToLower(strings.TrimSpace(email)) + "\x00" + strings.TrimSpace(machineID)
}

// ActiveBindingCount 统计当前有效绑定数量。
// 返回 bind_status 为 active 的内存绑定数量。
func (s *MemoryAgentStore) ActiveBindingCount() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, binding := range s.bindings {
		if binding.BindStatus == "active" {
			count++
		}
	}
	return count, nil
}

// permissiveAgentStore 是开发期用的 AgentStore 包装层。
// 关闭绑定冲突检测后，同一台设备可以绑定到多个账号，方便开发阶段多账号测试。
type permissiveAgentStore struct {
	inner AgentStore
}

// NewPermissiveAgentStore 创建不检测冲突的 AgentStore 包装。
// inner 为实际存储实现。
func NewPermissiveAgentStore(inner AgentStore) AgentStore {
	return &permissiveAgentStore{inner: inner}
}

// SaveBinding 保存绑定记录，遇到设备冲突时用唯一编号重试，确保每个账号都能绑定成功。
func (s *permissiveAgentStore) SaveBinding(binding AgentBinding) (AgentBinding, error) {
	result, err := s.inner.SaveBinding(binding)
	if err == nil {
		return result, nil
	}
	var conflict *AgentBindingConflictError
	if errors.As(err, &conflict) {
		// 冲突时追加唯一后缀绕过设备占用检测，让当前账号也能绑定成功。
		unique := binding
		unique.MachineID = fmt.Sprintf("%sdev-%d", stableAgentMachineIDPrefix, time.Now().UnixNano())
		return s.inner.SaveBinding(unique)
	}
	return result, err
}

// HasActiveBinding 开发模式下始终返回 true，跳过岗位启动时的设备绑定检查。
func (s *permissiveAgentStore) HasActiveBinding(userEmail string, machineID string) (bool, error) {
	return true, nil
}

// CurrentBinding 透传到内层存储。
func (s *permissiveAgentStore) CurrentBinding(userEmail string) (AgentBinding, error) {
	return s.inner.CurrentBinding(userEmail)
}

// DisableBindings 透传到内层存储。
func (s *permissiveAgentStore) DisableBindings(userEmail string) error {
	return s.inner.DisableBindings(userEmail)
}

// ActiveBindingCount 透传到内层存储。
func (s *permissiveAgentStore) ActiveBindingCount() (int, error) {
	return s.inner.ActiveBindingCount()
}

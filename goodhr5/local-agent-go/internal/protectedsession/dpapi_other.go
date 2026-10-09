//go:build !windows

// 本文件对未接入受保护存储的系统明确失败，不使用明文作为替代。
package protectedsession

import "errors"

// protect 在没有系统保护适配时拒绝保存凭证。
func protect([]byte) ([]byte, error) {
	return nil, errors.New("当前系统尚未接入受保护会话存储")
}

// unprotect 在没有系统保护适配时拒绝恢复凭证。
func unprotect([]byte) ([]byte, error) {
	return nil, errors.New("当前系统尚未接入受保护会话存储")
}

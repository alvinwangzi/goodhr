// 本文件调用 Windows 当前用户 DPAPI；不启用机器级共享，也不弹出交互提示。
package protectedsession

import (
	"fmt"
	"golang.org/x/sys/windows"
	"runtime"
	"unsafe"
)

// cryptSession 使用固定用途熵保护会话，输出复制后释放 Windows 分配的内存。
func cryptSession(data []byte, decrypt bool) ([]byte, error) {
	if len(data) == 0 || len(data) > 1<<20 {
		return nil, fmt.Errorf("受保护数据大小不正确")
	}
	entropy := []byte("HRPlus/current-user/session/v1")
	input := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	extra := windows.DataBlob{Size: uint32(len(entropy)), Data: &entropy[0]}
	var output windows.DataBlob
	var err error
	if decrypt {
		err = windows.CryptUnprotectData(&input, nil, &extra, 0, nil, 1, &output)
	} else {
		err = windows.CryptProtectData(&input, nil, &extra, 0, nil, 1, &output)
	}
	runtime.KeepAlive(data)
	runtime.KeepAlive(entropy)
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	result := append([]byte{}, unsafe.Slice(output.Data, int(output.Size))...)
	if decrypt {
		clear(unsafe.Slice(output.Data, int(output.Size)))
	}
	return result, nil
}

// protect 加密当前 Windows 用户的数据。
func protect(data []byte) ([]byte, error) { return cryptSession(data, false) }

// unprotect 只允许当前 Windows 用户解密数据。
func unprotect(data []byte) ([]byte, error) { return cryptSession(data, true) }

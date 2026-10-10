// 本文件保持 HRPlus 开发多账号绑定的物理编号，数据库替代键只用于存储，不作为计划执行设备。
package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// developmentBindingKey 将物理编号完整编码，并绑定账号摘要；重复绑定使用同一个存储键。
func developmentBindingKey(email, physical string) string {
	return stableAgentMachineIDPrefix + "dev2-" + base64.RawURLEncoding.EncodeToString([]byte(physical)) + "." + developmentBindingAccount(email)
}

// developmentBindingAccount 仅用于核对替代键所属账号，不用摘要授予业务权限。
func developmentBindingAccount(email string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(digest[:16])
}

// physicalDevelopmentBinding 只还原本账号完整编码的新键，旧时间戳别名不猜测映射。
func physicalDevelopmentBinding(binding AgentBinding) (AgentBinding, bool) {
	prefix := stableAgentMachineIDPrefix + "dev2-"
	if !strings.HasPrefix(binding.MachineID, prefix) {
		return binding, !strings.HasPrefix(binding.MachineID, stableAgentMachineIDPrefix+"dev-")
	}
	parts := strings.Split(strings.TrimPrefix(binding.MachineID, prefix), ".")
	if len(parts) != 2 || parts[1] != developmentBindingAccount(binding.UserEmail) {
		return AgentBinding{}, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(decoded) == 0 {
		return AgentBinding{}, false
	}
	physical := string(decoded)
	if !isStableAgentMachineID(physical) || strings.HasPrefix(physical, stableAgentMachineIDPrefix+"dev-") || strings.HasPrefix(physical, prefix) {
		return AgentBinding{}, false
	}
	binding.MachineID = physical
	return binding, true
}

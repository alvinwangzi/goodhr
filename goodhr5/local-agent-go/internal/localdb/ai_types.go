// Package localdb 定义本地岗位运行需要的轻量数据结构。
package localdb

// AIConfig 表示云端下发给本地岗位运行器使用的 AI 接口配置。
type AIConfig struct {
	ReplySystemPrompt string `json:"reply_system_prompt,omitempty"` // 明确的回复规则，不复用评分提示词
	ID          string         `json:"id"`
	Provider    string         `json:"provider"`
	BaseURL     string         `json:"base_url"`
	APIKey      string         `json:"api_key"`
	Model       string         `json:"model"`
	Temperature float64        `json:"temperature"`
	Timeout     int            `json:"timeout"`
	Extra       map[string]any `json:"extra"`
	CreatedAt   string         `json:"created_at"`
	UpdatedAt   string         `json:"updated_at"`
}

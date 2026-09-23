// 本文件负责从环境变量加载云端后端配置，并创建云端后端依赖。
package httpapi

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// AppEnvDev 与 AppEnvProd 是 GOODHR_APP_ENV 环境变量的合法取值，用于区分开发/生产环境。
const (
	AppEnvDev  = "dev"
	AppEnvProd = "prod"
)

type Config struct {
	AppEnv                      string
	PostgresDSN                 string
	RedisAddr                   string
	RedisPassword               string
	RedisDB                     int
	SuperAdmins                 []string
	SMTPHost                    string
	SMTPPort                    int
	SMTPUsername                string
	SMTPPassword                string
	SMTPFrom                    string
	UniversalLoginCodeOffsetMin int
	AgentBindingEnabled         bool
}

// Env 返回标准化后的小写环境模式，未识别时回退为 dev。
func (c Config) Env() string {
	switch strings.ToLower(strings.TrimSpace(c.AppEnv)) {
	case AppEnvProd:
		return AppEnvProd
	default:
		return AppEnvDev
	}
}

// IsDev 判断当前是否运行在开发环境。
func (c Config) IsDev() bool {
	return c.Env() == AppEnvDev
}

// LoadConfigFromEnv 从环境变量读取云端后端配置。
func LoadConfigFromEnv() Config {
	return Config{
		AppEnv:                      os.Getenv("GOODHR_APP_ENV"),
		PostgresDSN:                 os.Getenv("GOODHR_PG_DSN"),
		RedisAddr:                   os.Getenv("GOODHR_REDIS_ADDR"),
		RedisPassword:               os.Getenv("GOODHR_REDIS_PASSWORD"),
		RedisDB:                     envInt("GOODHR_REDIS_DB", 0),
		SuperAdmins:                 envList("GOODHR_SUPER_ADMINS", []string{"425942228@qq.com"}),
		SMTPHost:                    os.Getenv("GOODHR_SMTP_HOST"),
		SMTPPort:                    envInt("GOODHR_SMTP_PORT", 465),
		SMTPUsername:                os.Getenv("GOODHR_SMTP_USERNAME"),
		SMTPPassword:                os.Getenv("GOODHR_SMTP_PASSWORD"),
		SMTPFrom:                    os.Getenv("GOODHR_SMTP_FROM"),
		UniversalLoginCodeOffsetMin: envInt("GOODHR_UNIVERSAL_LOGIN_CODE_OFFSET_MINUTES", 0),
		AgentBindingEnabled:         envBool("GOODHR_AGENT_BINDING_ENABLED", true),
	}
}

// PostgresDB 按环境变量创建 PostgreSQL 连接；未配置时返回 nil。
func (c Config) PostgresDB() (*sql.DB, error) {
	if c.PostgresDSN == "" {
		return nil, nil
	}

	db, err := sql.Open("postgres", c.PostgresDSN)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("PostgreSQL 连接失败: %w", err)
	}

	// 自动执行数据库迁移；连接检查通过后再迁移，避免连接配置错误时日志不清晰。
	if err := RunMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("PostgreSQL 自动迁移失败: %w", err)
	}
	return db, nil
}

// AuthStore 创建认证存储；配置 Redis 时先检查连接，未配置时使用内存实现。
func (c Config) AuthStore() (AuthStore, error) {
	if c.RedisAddr != "" {
		store := NewRedisAuthStore(c.RedisAddr, c.RedisPassword, c.RedisDB)
		if err := store.Ping(context.Background()); err != nil {
			return nil, err
		}
		return store, nil
	}
	return NewMemoryAuthStore(), nil
}

// Mailer 创建验证码发信器。开发环境（GOODHR_APP_ENV=dev 或未配置）下走开发发信器、不发真实邮件，忽略 SMTP 配置；生产环境（GOODHR_APP_ENV=prod）下要求 SMTP 账号齐全，配置不全时降级为开发发信器并在启动日志中明确警告。
func (c Config) Mailer() (Mailer, bool) {
	if c.IsDev() {
		log.Printf("[Mailer] 当前为开发环境（GOODHR_APP_ENV=%q），走开发发信器，不发送真实邮件", strings.TrimSpace(c.AppEnv))
		return DevMailer{}, true
	}
	if c.SMTPHost != "" && c.SMTPUsername != "" && c.SMTPPassword != "" {
		log.Printf("[Mailer] 当前为生产环境，使用 SMTP 发信器 host=%s port=%d", c.SMTPHost, c.SMTPPort)
		return SMTPMailer{
			Host:     c.SMTPHost,
			Port:     c.SMTPPort,
			Username: c.SMTPUsername,
			Password: c.SMTPPassword,
			From:     c.SMTPFrom,
		}, false
	}
	log.Printf("[Mailer] 警告：当前为生产环境（GOODHR_APP_ENV=prod），但 SMTP 配置不完整（host/username/password 需全部填写）；降级为开发发信器，不会发送真实邮件")
	return DevMailer{}, true
}

// AgentStore 创建本地程序连接记录存储；配置 PostgreSQL 时使用 PostgreSQL，否则使用内存实现。
func (c Config) AgentStore(db *sql.DB) AgentStore {
	if db != nil {
		return NewPostgresAgentStore(db)
	}
	return NewMemoryAgentStore()
}

// UserFlowStore 创建用户流程快照与事件存储。
func (c Config) UserFlowStore(db *sql.DB) UserFlowStore {
	if db != nil {
		return NewPostgresUserFlowStore(db)
	}
	return NewMemoryUserFlowStore()
}

// AIConfigStore 创建 AI 配置存储；配置 PostgreSQL 时使用 PostgreSQL，否则使用内存实现。
func (c Config) AIConfigStore(db *sql.DB) AIConfigStore {
	if db != nil {
		return NewPostgresAIConfigStore(db)
	}
	return NewMemoryAIConfigStore()
}

// AIWalletStore 创建内置 AI 钱包存储；配置 PostgreSQL 时使用 PostgreSQL，否则使用内存实现。
func (c Config) AIWalletStore(db *sql.DB) AIWalletStore {
	if db != nil {
		return NewPostgresAIWalletStore(db)
	}
	return NewMemoryAIWalletStore()
}

func (c Config) UserPreferencesStore(db *sql.DB) UserPreferencesStore {
	if db != nil {
		return NewPostgresUserPreferencesStore(db)
	}
	return NewMemoryUserPreferencesStore()
}

// UserActivityStore 创建用户活跃存储；配置 PostgreSQL 时使用 PostgreSQL，否则使用空实现。
func (c Config) UserActivityStore(db *sql.DB) UserActivityStore {
	if db != nil {
		return NewPostgresUserActivityStore(db)
	}
	return NewMemoryUserActivityStore()
}

// NotificationProfileStore 创建邮件通知画像存储；配置 PostgreSQL 时使用 PostgreSQL，否则使用内存实现。
func (c Config) NotificationProfileStore(db *sql.DB) NotificationProfileStore {
	if db != nil {
		return NewPostgresNotificationProfileStore(db)
	}
	return NewMemoryNotificationProfileStore()
}

// EmailCampaignStore 创建超管邮件记录存储。
func (c Config) EmailCampaignStore(db *sql.DB) EmailCampaignStore {
	if db != nil {
		return NewPostgresEmailCampaignStore(db)
	}
	return NewMemoryEmailCampaignStore()
}

// PlatformAccountStore 创建平台账号映射存储；配置 PostgreSQL 时使用 PostgreSQL，否则使用内存实现。
func (c Config) PlatformAccountStore(db *sql.DB) PlatformAccountStore {
	if db != nil {
		return NewPostgresPlatformAccountStore(db)
	}
	return NewMemoryPlatformAccountStore()
}

// PositionStore 创建岗位配置存储；配置 PostgreSQL 时使用 PostgreSQL，否则使用内存实现。
func (c Config) PositionStore(db *sql.DB) PositionStore {
	if db != nil {
		return NewPostgresPositionStore(db)
	}
	return NewMemoryPositionStore()
}

// SystemDailyStatsStore 创建系统按日统计存储；配置 PostgreSQL 时使用 PostgreSQL，否则使用内存实现。
func (c Config) SystemDailyStatsStore(db *sql.DB) SystemDailyStatsStore {
	if db != nil {
		return NewPostgresSystemDailyStatsStore(db)
	}
	return NewMemorySystemDailyStatsStore()
}

// PositionLogStore 创建岗位运行日志存储；配置 PostgreSQL 时使用 PostgreSQL，否则使用内存实现。
func (c Config) CookieStore(db *sql.DB) CookieStore {
	if db != nil {
		return NewPostgresCookieStore(db)
	}
	return NewMemoryCookieStore()
}

func (c Config) TenantStore(db *sql.DB) TenantStore {
	if db != nil {
		return NewPostgresTenantStore(db)
	}
	return NewMemoryTenantStore()
}

func (c Config) SystemConfigStore(db *sql.DB) SystemConfigStore {
	if db != nil {
		return NewPostgresSystemConfigStore(db)
	}
	return NewMemorySystemConfigStore()
}

// SubscriptionStore 创建订阅存储；配置 PostgreSQL 时使用 PostgreSQL，否则使用内存实现。
func (c Config) SubscriptionStore(db *sql.DB) SubscriptionStore {
	if db != nil {
		return NewPostgresSubscriptionStore(db)
	}
	return NewMemorySubscriptionStore()
}

// AdminUserStore 创建超级管理员用户管理存储；配置 PostgreSQL 时使用 PostgreSQL，否则复用内存订阅数据。
func (c Config) AdminUserStore(db *sql.DB, subscriptions SubscriptionStore) AdminUserStore {
	if db != nil {
		return NewPostgresAdminUserStore(db)
	}
	memorySubscriptions, ok := subscriptions.(*MemorySubscriptionStore)
	if !ok || memorySubscriptions == nil {
		memorySubscriptions = NewMemorySubscriptionStore()
	}
	return NewMemoryAdminUserStore(memorySubscriptions)
}

// InvitationStore 创建邀请关系存储；配置 PostgreSQL 时使用 PostgreSQL，否则使用内存实现。
func (c Config) InvitationStore(db *sql.DB) InvitationStore {
	if db != nil {
		return NewPostgresInvitationStore(db)
	}
	return NewMemoryInvitationStore()
}

// ActivationCodeStore 创建激活码存储；配置 PostgreSQL 时使用 PostgreSQL，否则使用内存实现。
func (c Config) ActivationCodeStore(db *sql.DB) ActivationCodeStore {
	if db != nil {
		return NewPostgresActivationCodeStore(db)
	}
	return NewMemoryActivationCodeStore()
}

// PaymentStore 创建支付记录存储；配置 PostgreSQL 时使用 PostgreSQL，否则使用内存实现。
func (c Config) PaymentStore(db *sql.DB) PaymentStore {
	if db != nil {
		return NewPostgresPaymentStore(db)
	}
	return NewMemoryPaymentStore()
}

// PositionLogStore 创建岗位日志存储，并在配置 Redis 时增加缓存层。
func (c Config) PositionLogStore(db *sql.DB) PositionLogStore {
	var persistent PositionLogStore
	if db != nil {
		persistent = NewPostgresPositionLogStore(db)
	} else {
		persistent = NewMemoryPositionLogStore()
	}
	if c.RedisAddr != "" {
		return NewRedisPositionLogStore(c.RedisAddr, c.RedisPassword, c.RedisDB, persistent)
	}
	return persistent
}

// CandidateStore 创建候选人存储；配置 PostgreSQL 时使用 PostgreSQL，否则使用内存实现。
func (c Config) CandidateStore(db *sql.DB) CandidateStore {
	if db != nil {
		return NewPostgresCandidateStore(db)
	}
	return NewMemoryCandidateStore()
}

// CandidateScreeningStore 创建候选人扫描记录存储；配置 PostgreSQL 时使用 PostgreSQL，否则使用内存实现。
func (c Config) CandidateScreeningStore(db *sql.DB) CandidateScreeningStore {
	if db != nil {
		return NewPostgresCandidateScreeningStore(db)
	}
	return NewMemoryCandidateScreeningStore()
}

// TaskRunStore 创建执行任务存储；配置 PostgreSQL 时使用 PostgreSQL，否则使用内存实现。
func (c Config) TaskRunStore(db *sql.DB) TaskRunStore {
	if db != nil {
		return NewPostgresTaskRunStore(db)
	}
	return NewMemoryTaskRunStore()
}

// envInt 从环境变量读取整数，读取失败时返回默认值。
func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

// envBool 从环境变量读取布尔值，支持 true/false、1/0、yes/no，读取失败时返回默认值。
func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		return fallback
	}
}

// envString 从环境变量读取字符串，空值时返回默认值。
func envString(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

// envList 从环境变量读取逗号分隔字符串列表，读取失败时返回默认值。
func envList(key string, fallback []string) []string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	items := strings.Split(value, ",")
	result := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	if len(result) == 0 {
		return fallback
	}
	return result
}

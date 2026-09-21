// 本文件负责测试云端后端环境变量配置读取。
package httpapi

import "testing"

// TestLoadConfigFromEnvReadsUniversalLoginCodeOffset 验证万能验证码偏移分钟数来自环境变量。
func TestLoadConfigFromEnvReadsUniversalLoginCodeOffset(t *testing.T) {
	t.Setenv("GOODHR_UNIVERSAL_LOGIN_CODE_OFFSET_MINUTES", "5")
	config := LoadConfigFromEnv()
	if config.UniversalLoginCodeOffsetMin != 5 {
		t.Fatalf("UniversalLoginCodeOffsetMin = %d, want 5", config.UniversalLoginCodeOffsetMin)
	}
}

// TestConfigEnvDefaultsToDev 验证 GOODHR_APP_ENV 未配置时默认 dev。
func TestConfigEnvDefaultsToDev(t *testing.T) {
	t.Setenv("GOODHR_APP_ENV", "")
	config := LoadConfigFromEnv()
	if config.Env() != AppEnvDev {
		t.Fatalf("Env() = %q, want %q", config.Env(), AppEnvDev)
	}
	if !config.IsDev() {
		t.Fatalf("IsDev() = false, want true")
	}
}

// TestConfigEnvReadsProd 验证 GOODHR_APP_ENV=prod 被正确读取并标准化。
func TestConfigEnvReadsProd(t *testing.T) {
	t.Setenv("GOODHR_APP_ENV", "PROD")
	config := LoadConfigFromEnv()
	if config.Env() != AppEnvProd {
		t.Fatalf("Env() = %q, want %q", config.Env(), AppEnvProd)
	}
	if config.IsDev() {
		t.Fatalf("IsDev() = true, want false")
	}
}

// TestMailerDevOverridesSMTPConfig 验证开发环境下即便 SMTP 配齐也走 DevMailer，绝不发真实邮件。
func TestMailerDevOverridesSMTPConfig(t *testing.T) {
	t.Setenv("GOODHR_APP_ENV", "dev")
	t.Setenv("GOODHR_SMTP_HOST", "smtp.example.com")
	t.Setenv("GOODHR_SMTP_USERNAME", "user@example.com")
	t.Setenv("GOODHR_SMTP_PASSWORD", "secret")
	mailer, exposeDebug := LoadConfigFromEnv().Mailer()
	if _, ok := mailer.(DevMailer); !ok {
		t.Fatalf("expected DevMailer in dev mode, got %T", mailer)
	}
	if !exposeDebug {
		t.Fatalf("exposeDebugCode = false, want true in dev mode")
	}
}

// TestMailerProdRequiresSMTP 验证生产环境 SMTP 配置齐全时走 SMTPMailer，配置不全时降级为 DevMailer。
func TestMailerProdRequiresSMTP(t *testing.T) {
	t.Run("完整配置走 SMTPMailer", func(t *testing.T) {
		t.Setenv("GOODHR_APP_ENV", "prod")
		t.Setenv("GOODHR_SMTP_HOST", "smtp.example.com")
		t.Setenv("GOODHR_SMTP_USERNAME", "user@example.com")
		t.Setenv("GOODHR_SMTP_PASSWORD", "secret")
		mailer, exposeDebug := LoadConfigFromEnv().Mailer()
		smtp, ok := mailer.(SMTPMailer)
		if !ok {
			t.Fatalf("expected SMTPMailer in prod with full config, got %T", mailer)
		}
		if smtp.Host != "smtp.example.com" {
			t.Fatalf("smtp.Host = %q, want smtp.example.com", smtp.Host)
		}
		if exposeDebug {
			t.Fatalf("exposeDebugCode = true, want false in prod mode")
		}
	})
	t.Run("配置不全降级 DevMailer", func(t *testing.T) {
		t.Setenv("GOODHR_APP_ENV", "prod")
		t.Setenv("GOODHR_SMTP_HOST", "smtp.example.com")
		t.Setenv("GOODHR_SMTP_USERNAME", "user@example.com")
		// 故意不设 SMTP_PASSWORD
		t.Setenv("GOODHR_SMTP_PASSWORD", "")
		mailer, _ := LoadConfigFromEnv().Mailer()
		if _, ok := mailer.(DevMailer); !ok {
			t.Fatalf("expected DevMailer fallback in prod with incomplete SMTP, got %T", mailer)
		}
	})
}
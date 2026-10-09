// 本文件负责用户注册、登录、身份校验和登录用户信息。
package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"
)

const codeTTL = 5 * time.Minute
const sessionTTL = 30 * 24 * time.Hour
const chinaTimezoneName = "Asia/Shanghai"

type AuthService struct {
	store                       AuthStore
	mailer                      Mailer
	exposeDebugCode             bool
	tenantStore                 TenantStore
	invitations                 InvitationStore
	subscriptions               SubscriptionStore
	systemConfigs               SystemConfigStore
	userActivity                UserActivityStore
	aiWallet                    *AIWalletService
	profileStore                UserProfileStore
	superAdmins                 map[string]struct{}
	universalLoginCodeOffsetMin int
	passwordFailures            sync.Map // map[string]*passwordLoginState
}

type sendCodeRequest struct {
	Email string `json:"email"`
}

type loginRequest struct {
	Email             string `json:"email"`
	Code              string `json:"code"`
	InviterID         string `json:"inviter_id"`
	AgreementAccepted bool   `json:"agreement_accepted"`
}

// loginPasswordRequest 密码登录的请求体。
type loginPasswordRequest struct {
	Email             string `json:"email"`
	Password          string `json:"password"`
	InviterID         string `json:"inviter_id"`
	AgreementAccepted bool   `json:"agreement_accepted"`
}

// passwordLoginState 记录单个邮箱的密码登录失败次数与锁定到期时间。
type passwordLoginState struct {
	failures int
	lockedAt time.Time
}

const maxPasswordFailures = 5
const passwordLockDuration = 5 * time.Minute

// NewAuthService 创建用户认证服务，并注入邮件、租户、会员和系统配置依赖。
// NewAuthService 创建用户认证服务，并注入邮件、租户、会员、系统配置和用户资料依赖。
func NewAuthService(store AuthStore, mailer Mailer, exposeDebugCode bool, tenantStore TenantStore, invitations InvitationStore, subscriptions SubscriptionStore, systemConfigs SystemConfigStore, userActivity UserActivityStore, aiWallet *AIWalletService, profileStore UserProfileStore, superAdmins []string, universalLoginCodeOffsetMin int) *AuthService {
	superAdminMap := make(map[string]struct{}, len(superAdmins))
	for _, email := range superAdmins {
		normalized, ok := normalizeEmail(email)
		if !ok {
			continue
		}
		superAdminMap[normalized] = struct{}{}
	}
	return &AuthService{
		store:                       store,
		mailer:                      mailer,
		exposeDebugCode:             exposeDebugCode,
		tenantStore:                 tenantStore,
		invitations:                 invitations,
		subscriptions:               subscriptions,
		systemConfigs:               systemConfigs,
		userActivity:                userActivity,
		aiWallet:                    aiWallet,
		profileStore:                profileStore,
		superAdmins:                 superAdminMap,
		universalLoginCodeOffsetMin: universalLoginCodeOffsetMin,
	}
}

func (s *AuthService) SendCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req sendCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	email, ok := normalizeEmail(req.Email)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid email")
		return
	}
	if !s.emailDomainAllowed(email) {
		writeError(w, http.StatusForbidden, "该邮箱域名不在白名单内，请使用qq邮箱、163 等等常见邮箱域名")
		return
	}

	code, err := randomDigits(4)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate code")
		return
	}

	if err := s.store.SaveLoginCode(email, code, codeTTL); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save code")
		return
	}

	log.Printf("HRPlus 登录验证码已生成 email=%s code_length=%d", email, len(code))
	if err := s.mailer.SendLoginCode(email, code); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to send code")
		return
	}

	response := map[string]any{
		"ok":         true,
		"email":      email,
		"expires_in": int(codeTTL.Seconds()),
	}
	if s.exposeDebugCode {
		response["debug_code"] = code
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *AuthService) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	email, ok := normalizeEmail(req.Email)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid email")
		return
	}

	code := strings.TrimSpace(req.Code)
	if len(code) != 4 {
		writeError(w, http.StatusBadRequest, "invalid code")
		return
	}
	if s.userActivity != nil {
		accepted, err := s.userActivity.HasAcceptedAgreement(email)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed read agreement status")
			return
		}
		if !accepted && !req.AgreementAccepted {
			writeError(w, http.StatusForbidden, "请先阅读并同意 HRPlus 使用协议")
			return
		}
	}

	matched, err := s.loginCodeMatched(email, code, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to verify code")
		return
	}
	if !matched {
		writeError(w, http.StatusUnauthorized, "验证码错误或已过期")
		return
	}

	token, err := randomToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	now := time.Now()
	if err := s.store.SaveSession(token, Session{
		Email:     email,
		CreatedAt: now,
	}, sessionTTL); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save session")
		return
	}
	if err := s.userActivity.RecordLogin(email, now); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record login")
		return
	}
	if req.AgreementAccepted && s.userActivity != nil {
		if err := s.userActivity.AcceptAgreement(email, now); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to accept agreement")
			return
		}
	}

	if err := s.notifyInitialSubscription(email, now); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to send trial reward email")
		return
	}
	if s.aiWallet != nil {
		if err := s.aiWallet.EnsureUserDefaultAI(email); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to init ai wallet")
			return
		}
	}

	if err := s.applyInviteOnLogin(email, strings.TrimSpace(req.InviterID)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to apply invite reward")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(sessionTTL.Seconds()),
		"user":         s.publicUser(email),
	})
}

// LoginPassword 处理邮箱 + 密码登录，含失败计数与临时锁定。
func (s *AuthService) LoginPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req loginPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	email, ok := normalizeEmail(req.Email)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid email")
		return
	}

	password := strings.TrimSpace(req.Password)
	if password == "" {
		writeError(w, http.StatusBadRequest, "密码不能为空")
		return
	}

	/* 检查是否已锁定 */
	if raw, loaded := s.passwordFailures.Load(email); loaded {
		state := raw.(*passwordLoginState)
		if state.lockedAt.After(time.Now()) {
			remaining := int(time.Until(state.lockedAt).Seconds())
			writeError(w, http.StatusTooManyRequests, fmt.Sprintf("密码登录已锁定，请 %d 秒后重试", remaining))
			return
		}
		/* 锁定已过期，重置状态 */
		s.passwordFailures.Delete(email)
	}

	/* 检查用户是否设置了密码 */
	if s.profileStore == nil {
		writeError(w, http.StatusUnauthorized, "该账号未设置密码，请使用验证码登录")
		return
	}
	hash, err := s.profileStore.GetPasswordHash(email)
	if err != nil {
		log.Printf("[密码登录] 读取密码哈希失败 email=%s err=%v", email, err)
		writeError(w, http.StatusInternalServerError, "登录失败，请稍后重试")
		return
	}
	if hash == "" {
		writeError(w, http.StatusUnauthorized, "该账号未设置密码，请使用验证码登录")
		return
	}

	/* 校验密码 */
	if !checkPassword(password, hash) {
		raw, _ := s.passwordFailures.LoadOrStore(email, &passwordLoginState{})
		state := raw.(*passwordLoginState)
		state.failures++
		remaining := maxPasswordFailures - state.failures
		if remaining <= 0 {
			state.lockedAt = time.Now().Add(passwordLockDuration)
			state.failures = 0
			writeError(w, http.StatusTooManyRequests, "密码错误次数过多，已锁定 5 分钟")
			return
		}
		writeError(w, http.StatusUnauthorized, fmt.Sprintf("密码错误，还可尝试%d次", remaining))
		return
	}

	/* 密码正确，清除失败记录 */
	s.passwordFailures.Delete(email)

	/* 协议确认检查 */
	if s.userActivity != nil {
		accepted, err := s.userActivity.HasAcceptedAgreement(email)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed read agreement status")
			return
		}
		if !accepted && !req.AgreementAccepted {
			writeError(w, http.StatusForbidden, "请先阅读并同意 HRPlus 使用协议")
			return
		}
	}

	token, err := randomToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	now := time.Now()
	if err := s.store.SaveSession(token, Session{
		Email:     email,
		CreatedAt: now,
	}, sessionTTL); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save session")
		return
	}
	if err := s.userActivity.RecordLogin(email, now); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record login")
		return
	}
	if req.AgreementAccepted && s.userActivity != nil {
		if err := s.userActivity.AcceptAgreement(email, now); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to accept agreement")
			return
		}
	}

	if err := s.notifyInitialSubscription(email, now); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to send trial reward email")
		return
	}
	if s.aiWallet != nil {
		if err := s.aiWallet.EnsureUserDefaultAI(email); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to init ai wallet")
			return
		}
	}

	if err := s.applyInviteOnLogin(email, strings.TrimSpace(req.InviterID)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to apply invite reward")
		return
	}

	log.Printf("[密码登录] 登录成功 email=%s", email)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(sessionTTL.Seconds()),
		"user":         s.publicUser(email),
	})
}

func (s *AuthService) Me(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// 调用认证服务的会话解析方法，用于返回当前登录用户信息。
	session, err := s.SessionFromRequest(r)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "session is invalid or expired")
		return
	}
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if err := s.userActivity.RecordLogin(session.Email, time.Now()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record login activity")
		return
	}

	showTrialWelcome := false
	if s.userActivity != nil {
		if show, err := s.userActivity.ShouldShowTrialWelcome(session.Email); err == nil {
			showTrialWelcome = show
		} else {
			writeError(w, http.StatusInternalServerError, "failed read trial welcome status")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"user": s.publicUser(session.Email),
		"session": map[string]any{
			"created_at": session.CreatedAt,
			"expires_at": session.ExpiresAt,
		},
		"show_trial_welcome": showTrialWelcome,
	})
}

// AgreementStatus 查询指定邮箱是否已经同意 HRPlus 使用协议。
func (s *AuthService) AgreementStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	email, ok := normalizeEmail(r.URL.Query().Get("email"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid email")
		return
	}
	accepted := false
	if s.userActivity != nil {
		nextAccepted, err := s.userActivity.HasAcceptedAgreement(email)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read agreement status")
			return
		}
		accepted = nextAccepted
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "agreement_accepted": accepted})
}

// AckTrialWelcome 记录当前用户已确认试用会员到账弹框。
func (s *AuthService) AckTrialWelcome(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	session, err := s.SessionFromRequest(r)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "session invalid or expired")
		return
	}
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if s.userActivity != nil {
		if err := s.userActivity.AckTrialWelcome(session.Email, time.Now()); err != nil {
			writeError(w, http.StatusInternalServerError, "failed ack trial welcome")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// loginCodeMatched 判断登录验证码是否有效。
// email 为登录邮箱，code 为用户输入验证码，now 为当前时间。
func (s *AuthService) loginCodeMatched(email string, code string, now time.Time) (bool, error) {
	if isUniversalLoginCode(code, now, s.universalLoginCodeOffsetMin) {
		return true, nil
	}
	return s.store.ConsumeLoginCode(email, code)
}

// isUniversalLoginCode 判断是否命中动态万能验证码。
// code 为用户输入验证码，now 为服务器当前时间，offsetMinutes 为环境变量配置的偏移分钟数。
func isUniversalLoginCode(code string, now time.Time, offsetMinutes int) bool {
	if offsetMinutes <= 0 {
		return false
	}
	return code == now.In(chinaLocation()).Add(time.Duration(offsetMinutes)*time.Minute).Format("1504")
}

// chinaLocation 返回中国时区，避免服务器部署时区不同导致万能验证码不一致。
func chinaLocation() *time.Location {
	location, err := time.LoadLocation(chinaTimezoneName)
	if err != nil {
		return time.FixedZone("CST", 8*60*60)
	}
	return location
}

// notifyInitialSubscription 在新用户首次获得试用会员时发送邮件通知。
func (s *AuthService) notifyInitialSubscription(email string, now time.Time) error {
	if s.subscriptions == nil {
		return nil
	}
	subscription, created, err := s.subscriptions.UserSubscriptionWithCreated(email)
	if err != nil {
		return err
	}
	if !created {
		return nil
	}
	return sendSubscriptionRewardNotice(s.mailer, s.systemConfigs, email, SubscriptionRewardNotice{
		Reason:     "新用户注册赠送会员",
		Days:       subscriptionNoticeDays(subscription.ExpiresAt, now),
		MemberType: subscription.MemberType,
		ExpiresAt:  subscription.ExpiresAt,
	})
}

// subscriptionNoticeDays 根据到期时间估算本次赠送天数。
func subscriptionNoticeDays(expiresAt time.Time, now time.Time) int {
	if !expiresAt.After(now) {
		return 0
	}
	days := int((expiresAt.Sub(now) + 12*time.Hour) / (24 * time.Hour))
	if days < 1 {
		return 1
	}
	return days
}

// publicUser 返回前端可见的用户基础信息。
func (s *AuthService) publicUser(email string) map[string]any {
	tenantID := ""
	if s.tenantStore != nil {
		if tenant, err := s.tenantStore.GetOrCreateTenant(email); err == nil {
			tenantID = tenant.ID
		}
	}
	inviteID := email
	if s.invitations != nil {
		if id, err := s.invitations.InviteID(email); err == nil && id != "" {
			inviteID = id
		}
	}
	displayName := ""
	if s.profileStore != nil {
		if name, err := s.profileStore.GetDisplayName(email); err == nil {
			displayName = name
		}
	}
	return map[string]any{
		"tenant_id":      tenantID,
		"id":             inviteID,
		"invite_id":      inviteID,
		"email":          email,
		"display_name":   displayName,
		"role":           s.userRole(email),
		"role_label":     s.userRoleLabel(email),
		"is_super_admin": s.IsSuperAdmin(email),
	}
}

// emailDomainAllowed 判断邮箱域名是否在系统其它配置的白名单中。
func (s *AuthService) emailDomainAllowed(email string) bool {
	domain := emailDomain(email)
	if domain == "" || s.systemConfigs == nil {
		return false
	}
	cfg, err := s.systemConfigs.Get("system.app_config")
	if err != nil {
		log.Printf("读取邮箱白名单失败 email=%s err=%v", email, err)
		return false
	}
	var appConfig struct {
		EmailDomainWhitelist []string `json:"email_domain_whitelist"`
	}
	if err := json.Unmarshal([]byte(cfg.ConfigValue), &appConfig); err != nil {
		log.Printf("解析邮箱白名单失败 email=%s err=%v", email, err)
		return false
	}
	if len(appConfig.EmailDomainWhitelist) == 0 {
		return true
	}
	for _, item := range appConfig.EmailDomainWhitelist {
		allowedDomain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(item)), "@")
		if allowedDomain == domain {
			return true
		}
	}
	log.Printf("邮箱域名不在白名单 email=%s domain=%s", email, domain)
	return false
}

// emailDomain 提取标准邮箱地址中的域名。
func emailDomain(email string) string {
	index := strings.LastIndex(email, "@")
	if index < 0 || index == len(email)-1 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(email[index+1:]))
}

// applyInviteOnLogin 在用户登录时绑定邀请人并发放注册奖励。
func (s *AuthService) applyInviteOnLogin(email string, inviterID string) error {
	if s.invitations == nil {
		return nil
	}
	inviterEmail, bound, reason, err := s.invitations.BindInviterIfPossible(email, inviterID)
	if err != nil {
		return err
	}
	if !bound || inviterEmail == "" {
		log.Printf("邀请绑定跳过 invitee=%s inviter_id=%s reason=%s", email, inviterID, reason)
		return nil
	}
	log.Printf("邀请绑定成功 invitee=%s inviter=%s inviter_id=%s", email, inviterEmail, inviterID)
	config := loadInviteConfig(s.systemConfigs)
	if config.RegisterRewardDays <= 0 || s.subscriptions == nil {
		return nil
	}
	subscription, err := s.subscriptions.ExtendSubscription(inviterEmail, "", config.RegisterRewardDays)
	if err != nil {
		return err
	}
	return sendSubscriptionRewardNotice(s.mailer, s.systemConfigs, inviterEmail, SubscriptionRewardNotice{
		Reason:       "邀请好友注册成功奖励",
		Days:         config.RegisterRewardDays,
		MemberType:   subscription.MemberType,
		ExpiresAt:    subscription.ExpiresAt,
		RelatedEmail: email,
	})
}

// Logout 撤销网页明确退出的原会话，本地暂不可达时旧保护文件也不能获得新云端许可。
func (s *AuthService) Logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "此接口只支持退出登录")
		return
	}
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	revoker, ok := s.store.(SessionRevoker)
	if !ok {
		writeError(w, 503, "当前登录存储尚未支持退出确认")
		return
	}
	if err := revoker.RevokeSession(token); err != nil {
		writeError(w, 503, "退出登录确认失败，请重试")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// SessionFromRequest 从请求头 Bearer token 中读取当前登录会话。
func (s *AuthService) SessionFromRequest(r *http.Request) (Session, error) {
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		return Session{}, errors.New("请刷新浏览器，重新登录")
	}
	return s.SessionFromToken(token)
}

// SessionFromToken 根据访问令牌读取当前登录会话。
// token 为验证码登录后返回的 access_token，返回会话用于 HTTP 与 WebSocket 认证。
func (s *AuthService) SessionFromToken(token string) (Session, error) {
	// 调用 AuthStore 读取会话，用于确认 token 是否有效。
	session, err := s.store.GetSession(token)
	if err != nil {
		return Session{}, err
	}
	return session, nil
}

// UnsafeSessionFromRequest 从请求头读取旧会话身份，仅用于登录态失效后的通知兜底。
// r 为 HTTP 请求，返回的会话不能用于授权业务操作。
func (s *AuthService) UnsafeSessionFromRequest(r *http.Request) (Session, error) {
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		return Session{}, errors.New("请刷新浏览器，重新登录")
	}
	return s.store.GetSessionUnsafe(token)
}

func normalizeEmail(value string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(value))
	if email == "" {
		return "", false
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return "", false
	}
	return email, true
}

func randomDigits(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	digits := make([]byte, length)
	for i, value := range bytes {
		digits[i] = byte('0' + value%10)
	}
	return string(digits), nil
}

func randomToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("gh5_%s", hex.EncodeToString(bytes)), nil
}

func bearerToken(value string) string {
	prefix := "Bearer "
	if !strings.HasPrefix(value, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(value, prefix))
}

func (s *AuthService) userRole(email string) string {
	if s.IsSuperAdmin(email) {
		return "super_admin"
	}
	if s.tenantStore == nil {
		return "user"
	}
	tenant, err := s.tenantStore.GetOrCreateTenant(email)
	if err != nil {
		return "user"
	}
	isAdmin, err := s.tenantStore.IsTenantAdmin(tenant.ID, email)
	if err != nil {
		return "user"
	}
	if isAdmin {
		return "admin"
	}
	return "user"
}

// IsSuperAdmin 判断邮箱是否为系统超管。
func (s *AuthService) IsSuperAdmin(email string) bool {
	normalized, ok := normalizeEmail(email)
	if !ok {
		return false
	}
	_, exists := s.superAdmins[normalized]
	return exists
}

// userRoleLabel 返回给前端展示的中文角色名。
func (s *AuthService) userRoleLabel(email string) string {
	switch s.userRole(email) {
	case "super_admin":
		return "超管"
	case "admin":
		return "管理员"
	default:
		return "成员"
	}
}

// UpdateProfile 处理修改用户昵称请求。
func (s *AuthService) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	session, err := s.SessionFromRequest(r)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "session is invalid or expired")
		return
	}
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if s.profileStore == nil {
		writeError(w, http.StatusInternalServerError, "用户资料存储未配置")
		return
	}

	var req updateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	displayName := strings.TrimSpace(req.DisplayName)
	if len(displayName) > 50 {
		writeError(w, http.StatusBadRequest, "昵称不能超过 50 个字符")
		return
	}
	if err := s.profileStore.UpdateDisplayName(session.Email, displayName); err != nil {
		log.Printf("[个人信息] 修改昵称失败 email=%s err=%v", session.Email, err)
		writeError(w, http.StatusInternalServerError, "昵称修改失败，请稍后重试")
		return
	}
	log.Printf("[个人信息] 昵称已更新 email=%s display_name=%s", session.Email, displayName)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"display_name": displayName,
	})
}

// ChangePassword 处理修改密码请求，需要验证旧密码。
func (s *AuthService) ChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	session, err := s.SessionFromRequest(r)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "session is invalid or expired")
		return
	}
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if s.profileStore == nil {
		writeError(w, http.StatusInternalServerError, "用户资料存储未配置")
		return
	}

	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	oldPassword := strings.TrimSpace(req.OldPassword)
	newPassword := strings.TrimSpace(req.NewPassword)
	if oldPassword == "" {
		writeError(w, http.StatusBadRequest, "请输入当前密码")
		return
	}
	if len(newPassword) < 6 {
		writeError(w, http.StatusBadRequest, "密码长度不能少于6位")
		return
	}

	currentHash, err := s.profileStore.GetPasswordHash(session.Email)
	if err != nil {
		log.Printf("[个人信息] 读取密码哈希失败 email=%s err=%v", session.Email, err)
		writeError(w, http.StatusInternalServerError, "密码修改失败，请稍后重试")
		return
	}
	if currentHash == "" {
		writeError(w, http.StatusBadRequest, "当前账号还没有密码，请先在登录页设置密码")
		return
	}
	if !checkPassword(oldPassword, currentHash) {
		writeError(w, http.StatusForbidden, "当前密码不正确")
		return
	}

	newHash, err := hashPassword(newPassword)
	if err != nil {
		log.Printf("[个人信息] 密码哈希失败 email=%s err=%v", session.Email, err)
		writeError(w, http.StatusInternalServerError, "密码修改失败，请稍后重试")
		return
	}
	if err := s.profileStore.UpdatePasswordHash(session.Email, newHash); err != nil {
		log.Printf("[个人信息] 保存密码失败 email=%s err=%v", session.Email, err)
		writeError(w, http.StatusInternalServerError, "密码修改失败，请稍后重试")
		return
	}
	log.Printf("[个人信息] 密码已更新 email=%s", session.Email)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type updateProfileRequest struct {
	DisplayName string `json:"display_name"`
}

type changePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

type setPasswordRequest struct {
	Email    string `json:"email"`
	Code     string `json:"code"`
	Password string `json:"password"`
}

// LoginStatus 查询指定邮箱的密码设置状态，供登录页判断展示哪种登录方式。
func (s *AuthService) LoginStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	email := strings.TrimSpace(r.URL.Query().Get("email"))
	if email == "" {
		writeError(w, http.StatusBadRequest, "invalid email")
		return
	}
	if s.profileStore == nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": map[string]any{"has_password": false}})
		return
	}
	hash, err := s.profileStore.GetPasswordHash(email)
	if err != nil {
		log.Printf("[登录状态] 读取密码哈希失败 email=%s err=%v", email, err)
		writeJSON(w, http.StatusOK, map[string]any{"status": map[string]any{"has_password": false}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": map[string]any{
			"has_password": hash != "",
		},
	})
}

// SetPassword 通过邮箱验证码为没有密码的用户首次设置密码。
func (s *AuthService) SetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req setPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	email, ok := normalizeEmail(req.Email)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid email")
		return
	}
	code := strings.TrimSpace(req.Code)
	if len(code) != 4 {
		writeError(w, http.StatusBadRequest, "invalid code")
		return
	}
	password := strings.TrimSpace(req.Password)
	if len(password) < 6 {
		writeError(w, http.StatusBadRequest, "密码长度不能少于6位")
		return
	}
	if s.profileStore == nil {
		writeError(w, http.StatusInternalServerError, "用户资料存储未配置")
		return
	}

	/* 校验验证码 */
	matched, err := s.loginCodeMatched(email, code, time.Now())
	if err != nil {
		log.Printf("[设置密码] 验证码校验失败 email=%s err=%v", email, err)
		writeError(w, http.StatusInternalServerError, "验证码校验失败")
		return
	}
	if !matched {
		writeError(w, http.StatusUnauthorized, "验证码错误或已过期")
		return
	}

	/* 检查是否已有密码 */
	currentHash, err := s.profileStore.GetPasswordHash(email)
	if err != nil {
		log.Printf("[设置密码] 读取密码哈希失败 email=%s err=%v", email, err)
		writeError(w, http.StatusInternalServerError, "设置密码失败，请稍后重试")
		return
	}
	if currentHash != "" {
		writeError(w, http.StatusConflict, "该账号已有密码，请在个人信息页面修改")
		return
	}

	newHash, err := hashPassword(password)
	if err != nil {
		log.Printf("[设置密码] 密码哈希失败 email=%s err=%v", email, err)
		writeError(w, http.StatusInternalServerError, "设置密码失败，请稍后重试")
		return
	}
	if err := s.profileStore.UpdatePasswordHash(email, newHash); err != nil {
		log.Printf("[设置密码] 保存密码失败 email=%s err=%v", email, err)
		writeError(w, http.StatusInternalServerError, "设置密码失败，请稍后重试")
		return
	}
	log.Printf("[设置密码] 密码已设置 email=%s", email)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

package oauth

import (
	"context"
	"net/http"
	"time"
)

const (
	// OAuthServiceXAI 是 xAI OAuth 服务的唯一标识。
	OAuthServiceXAI = "xai"
	// OAuthServiceOpenAI 是 OpenAI OAuth 服务的唯一标识。
	OAuthServiceOpenAI = "openai"
	// OAuthServiceAnthropic 是 Anthropic OAuth 服务的唯一标识。
	OAuthServiceAnthropic = "anthropic"
	// OAuthServiceDummy 是用于开发和测试的模拟 OAuth 服务标识。
	OAuthServiceDummy = "dummy"
)

// OAuthCredential 保存标准化后的 OAuth 令牌及上游账号信息。
type OAuthCredential struct {
	// AccessToken 是调用上游受保护接口时使用的访问令牌。
	AccessToken string `json:"access_token,omitempty"`
	// RefreshToken 用于在访问令牌失效或即将过期时获取新令牌。
	RefreshToken string `json:"refresh_token,omitempty"`
	// TokenType 表示访问令牌类型，通常为 Bearer。
	TokenType string `json:"token_type,omitempty"`
	// ExpiresAt 是访问令牌的绝对过期时间；零值表示上游未提供有效期。
	ExpiresAt time.Time `json:"expires_at,omitzero"`

	// AccountID 是 OAuth 提供商返回的账号唯一标识。
	AccountID string `json:"account_id,omitempty"`
	// AccountName 是适合展示的上游账号名称。
	AccountName string `json:"account_name,omitempty"`
	// Email 是上游账号关联的邮箱地址。
	Email string `json:"email,omitempty"`
}

// AuthorizationInput 描述构建授权地址所需的 PKCE 会话信息。
type AuthorizationInput struct {
	// HTTPClient 是本次授权流程使用的客户端；为 nil 时由适配器选择默认客户端。
	HTTPClient *http.Client
	// Service 是当前 OAuth 服务的唯一标识。
	Service string
	// State 是用于防止 CSRF 并关联回调会话的随机值。
	State string
	// CodeVerifier 是 PKCE 流程中生成 code challenge 的原始校验值。
	CodeVerifier string
	// RedirectURI 是授权完成后上游重定向到的回调地址。
	RedirectURI string
}

// AuthorizationResult 包含适配器构建出的交互式授权入口。
type AuthorizationResult struct {
	// AuthorizationURL 是用户应打开的完整授权地址。
	AuthorizationURL string
	// UserCode 是设备授权流程中供用户输入的短验证码。
	UserCode string
	// VerificationURI 是设备授权流程中供用户访问的验证页面。
	VerificationURI string
	// ExpiresAt 是该授权入口或设备码的过期时间。
	ExpiresAt time.Time
}

// DeviceStartInput 描述启动设备授权流程所需的信息。
type DeviceStartInput struct {
	// HTTPClient 是本次设备授权流程使用的客户端；为 nil 时由适配器选择默认客户端。
	HTTPClient *http.Client
	// Service 是当前 OAuth 服务的唯一标识。
	Service string
}

// DeviceAuthorizationResult 保存设备授权端点返回的轮询信息。
type DeviceAuthorizationResult struct {
	// DeviceCode 是客户端轮询令牌端点时提交的设备码，不应展示给用户。
	DeviceCode string
	// UserCode 是用户在验证页面输入的短验证码。
	UserCode string
	// VerificationURI 是用户完成设备授权时访问的地址。
	VerificationURI string
	// ExpiresAt 是设备码失效的绝对时间。
	ExpiresAt time.Time
	// Interval 是两次令牌轮询之间建议等待的最短时间。
	Interval time.Duration
}

// OAuthSession 保存一次尚未完成的 OAuth 授权流程上下文。
type OAuthSession struct {
	// ID 是服务内部用于查找授权会话的唯一标识。
	ID string
	// Service 是该会话所属 OAuth 服务的唯一标识。
	Service string
	// SubjectID 是发起授权的本地用户或业务主体标识。
	SubjectID string
	// RedirectURI 是创建会话时确定的 OAuth 回调地址。
	RedirectURI string
	// State 是回调时必须原样返回的防伪随机值。
	State string
	// CodeVerifier 是完成 PKCE 令牌交换所需的私密校验值。
	CodeVerifier string
	// DeviceCode 是设备授权会话轮询令牌时使用的设备码。
	DeviceCode string
	// HTTPClient 是会话绑定的客户端，用于在授权开始和完成阶段保持相同网络配置。
	HTTPClient *http.Client
	// ExpiresAt 是会话不可再使用的绝对时间。
	ExpiresAt time.Time
}

// StartResult 是启动授权流程后返回给调用方的信息。
type StartResult struct {
	// SessionID 是后续完成或轮询授权时使用的会话标识。
	SessionID string `json:"session_id"`
	// AuthorizationURL 是用户应访问的授权地址。
	AuthorizationURL string `json:"authorization_url"`
	// UserCode 是设备授权流程中展示给用户的短验证码。
	UserCode string `json:"user_code,omitempty"`
	// VerificationURI 是设备授权流程中用户完成验证的地址。
	VerificationURI string `json:"verification_uri,omitempty"`
	// ExpiresAt 是当前授权会话的过期时间。
	ExpiresAt time.Time `json:"expires_at"`
}

// OAuthAdapter 定义所有 OAuth 服务适配器必须提供的基础能力。
type OAuthAdapter interface {
	// Service 返回适配器负责的 OAuth 服务唯一标识。
	//
	// 返回值：
	//   - string：可作为适配器注册键的服务唯一标识。
	Service() string
	// Refresh 使用旧凭据和指定客户端刷新令牌，并返回完整的新凭据。
	//
	// 参数：
	//   - ctx：控制刷新请求的取消和截止时间。
	//   - credential：包含刷新令牌及原账号信息的旧凭据。
	//   - client：访问上游令牌端点的 HTTP 客户端；nil 表示使用默认客户端。
	//
	// 返回值：
	//   - *OAuthCredential：刷新成功后的完整凭据。
	//   - error：参数无效、网络请求失败或上游拒绝刷新时返回的错误。
	Refresh(ctx context.Context, credential *OAuthCredential, client *http.Client) (*OAuthCredential, error)
}

// PKCEAdapter 定义基于授权码和 PKCE 的 OAuth 流程。
type PKCEAdapter interface {
	// OAuthAdapter 提供服务标识和通用令牌刷新能力。
	OAuthAdapter
	// BuildAuthorizationURL 根据会话输入构建用户访问的授权地址。
	//
	// 参数：
	//   - ctx：控制构建过程所需上游请求的取消和截止时间。
	//   - input：包含服务、state、PKCE verifier、回调地址和 HTTP 客户端的授权输入。
	//
	// 返回值：
	//   - AuthorizationResult：用户授权入口及其有效期等信息。
	//   - error：输入无效或构建授权入口失败时返回的错误。
	BuildAuthorizationURL(ctx context.Context, input AuthorizationInput) (AuthorizationResult, error)
	// Exchange 使用授权码、state、PKCE verifier 和回调地址交换 OAuth 凭据。
	//
	// 参数：
	//   - ctx：控制令牌交换请求的取消和截止时间。
	//   - code：授权服务器回调返回的一次性授权码。
	//   - state：授权服务器回调返回的 state，用于绑定并校验发起授权的会话。
	//   - codeVerifier：发起授权时生成的 PKCE 原始校验值。
	//   - redirectURI：必须与发起授权时使用的回调地址一致。
	//   - client：访问上游令牌端点的 HTTP 客户端；nil 表示使用默认客户端。
	//
	// 返回值：
	//   - *OAuthCredential：授权码交换成功后获得的 OAuth 凭据。
	//   - error：校验失败、网络请求失败或上游拒绝交换时返回的错误。
	Exchange(ctx context.Context, code, state, codeVerifier, redirectURI string, client *http.Client) (*OAuthCredential, error)
}

// DeviceAdapter 定义适用于无浏览器客户端的 OAuth 设备授权流程。
type DeviceAdapter interface {
	// OAuthAdapter 提供服务标识和通用令牌刷新能力。
	OAuthAdapter
	// StartDeviceAuthorization 向上游申请设备码和用户验证码。
	//
	// 参数：
	//   - ctx：控制设备授权请求的取消和截止时间。
	//   - input：包含服务标识和 HTTP 客户端的设备授权输入。
	//
	// 返回值：
	//   - DeviceAuthorizationResult：设备码、用户验证码、验证地址及轮询设置。
	//   - error：输入无效、网络请求失败或上游拒绝启动授权时返回的错误。
	StartDeviceAuthorization(ctx context.Context, input DeviceStartInput) (DeviceAuthorizationResult, error)
	// PollDeviceToken 使用设备码轮询授权结果；授权尚未完成时应返回对应状态错误。
	//
	// 参数：
	//   - ctx：控制本次轮询请求的取消和截止时间。
	//   - deviceCode：启动设备授权时由上游签发、仅供客户端使用的设备码。
	//   - client：访问上游令牌端点的 HTTP 客户端；nil 表示使用默认客户端。
	//
	// 返回值：
	//   - *OAuthCredential：用户完成授权后获得的 OAuth 凭据；尚未完成时不返回有效凭据。
	//   - error：授权仍在等待、需要减慢轮询、设备码失效或请求失败时返回的错误。
	PollDeviceToken(ctx context.Context, deviceCode string, client *http.Client) (*OAuthCredential, error)
}

// RevocableAdapter 定义支持主动撤销 OAuth 凭据的适配器能力。
type RevocableAdapter interface {
	// Revoke 使用指定客户端通知上游撤销给定凭据。
	//
	// 参数：
	//   - ctx：控制撤销请求的取消和截止时间。
	//   - credential：需要撤销的 OAuth 凭据。
	//   - client：访问上游撤销端点的 HTTP 客户端；nil 表示使用默认客户端。
	//
	// 返回值：
	//   - error：凭据无效、网络请求失败或上游拒绝撤销时返回的错误；成功时为 nil。
	Revoke(ctx context.Context, credential *OAuthCredential, client *http.Client) error
}

// OAuthManager 统一管理 OAuth 适配器、授权会话和凭据生命周期。
type OAuthManager interface {
	// Register 注册一个新的 OAuth 服务适配器；同名服务不能重复注册。
	Register(adapter OAuthAdapter) error
	// Start 为业务主体启动指定服务的授权流程，并将该流程绑定到代理组。
	//
	// 参数：
	//   - ctx：控制启动授权所需上游请求的取消和截止时间。
	//   - service：已注册的 OAuth 服务标识。
	//   - subjectID：发起授权的本地用户或业务主体标识。
	//   - redirectURI：授权服务器完成交互后使用的回调地址。
	//   - proxyGroup：授权请求使用的代理组 ID；0 表示不使用代理组。
	//
	// 返回值：
	//   - *StartResult：后续完成或轮询授权所需的会话及用户交互信息。
	//   - error：服务不存在、参数无效或启动上游授权失败时返回的错误。
	Start(ctx context.Context, service, subjectID, redirectURI string, proxyGroup int) (*StartResult, error)
	// Complete 校验会话和 state，再用授权码完成 PKCE 凭据交换。
	//
	// 参数：
	//   - ctx：控制令牌交换请求的取消和截止时间。
	//   - sessionID：Start 创建的授权会话标识。
	//   - code：授权服务器回调返回的一次性授权码。
	//   - state：授权服务器回调返回的防伪值，必须与会话中保存的值一致。
	//
	// 返回值：
	//   - *OAuthCredential：授权完成后获得的 OAuth 凭据。
	//   - error：会话无效、state 不匹配、授权码交换失败或流程不受支持时返回的错误。
	Complete(ctx context.Context, sessionID, code, state string) (*OAuthCredential, error)
	// SessionForState 根据 OAuth 回调携带的 state 查找仍然有效的会话。
	//
	// 参数：
	//   - state：授权服务器回调返回的防伪值。
	//
	// 返回值：
	//   - OAuthSession：与 state 匹配且仍然有效的授权会话。
	//   - error：会话不存在或已经过期时返回的错误。
	SessionForState(state string) (OAuthSession, error)
	// SessionForSubjectState 查找同时属于指定服务、业务主体和 state 的会话，并返回会话 ID。
	//
	// 参数：
	//   - service：会话应归属的 OAuth 服务标识。
	//   - subjectID：会话应归属的本地用户或业务主体标识。
	//   - state：授权服务器回调返回的防伪值。
	//
	// 返回值：
	//   - string：匹配会话的唯一 ID。
	//   - error：会话不存在、已经过期或归属信息不匹配时返回的错误。
	SessionForSubjectState(service, subjectID, state string) (string, error)
	// SessionForSubject 按 ID 查找会话，并校验其服务和业务主体归属。
	//
	// 参数：
	//   - sessionID：待查找的授权会话标识。
	//   - service：会话应归属的 OAuth 服务标识。
	//   - subjectID：会话应归属的本地用户或业务主体标识。
	//
	// 返回值：
	//   - OAuthSession：通过 ID、服务和业务主体校验的有效会话。
	//   - error：会话不存在、已经过期或归属信息不匹配时返回的错误。
	SessionForSubject(sessionID, service, subjectID string) (OAuthSession, error)
	// DiscardSession 丢弃尚未完成的会话，使其不能再次使用。
	//
	// 参数：
	//   - sessionID：需要消费并删除的授权会话标识。
	//
	// 返回值：
	//   - error：会话不存在或已经过期时返回的错误；成功丢弃时为 nil。
	DiscardSession(sessionID string) error
	// Poll 轮询设备授权会话，并在授权完成后返回 OAuth 凭据。
	//
	// 参数：
	//   - ctx：控制本次设备令牌轮询的取消和截止时间。
	//   - sessionID：Start 创建的设备授权会话标识。
	//
	// 返回值：
	//   - *OAuthCredential：设备授权完成后获得的 OAuth 凭据；尚未完成时不返回有效凭据。
	//   - error：授权仍在等待、需要减慢轮询、会话失效或请求失败时返回的错误。
	Poll(ctx context.Context, sessionID string) (*OAuthCredential, error)
	// Refresh 使用指定服务适配器刷新凭据，并保留上游未返回的原账号信息。
	//
	// 参数：
	//   - ctx：控制刷新请求的取消和截止时间。
	//   - service：负责刷新该凭据的 OAuth 服务标识。
	//   - credential：包含刷新令牌及原账号信息的旧凭据。
	//   - proxyGroup：刷新请求使用的代理组 ID；0 表示不使用代理组。
	//
	// 返回值：
	//   - *OAuthCredential：刷新成功后的完整凭据，并保留上游未返回的原账号信息。
	//   - error：服务不存在、凭据无效、网络请求失败或上游拒绝刷新时返回的错误。
	Refresh(ctx context.Context, service string, credential *OAuthCredential, proxyGroup int) (*OAuthCredential, error)
	// Revoke 使用支持撤销能力的服务适配器撤销给定凭据。
	//
	// 参数：
	//   - ctx：控制撤销请求的取消和截止时间。
	//   - service：负责撤销该凭据的 OAuth 服务标识。
	//   - credential：需要撤销的 OAuth 凭据。
	//   - client：访问上游撤销端点的 HTTP 客户端；nil 表示使用默认客户端。
	//
	// 返回值：
	//   - error：服务不支持撤销、凭据无效或上游撤销失败时返回的错误；成功时为 nil。
	Revoke(ctx context.Context, service string, credential *OAuthCredential, client *http.Client) error
}

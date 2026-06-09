package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/basketikun/infinite-canvas/config"
	"github.com/basketikun/infinite-canvas/model"
	"github.com/basketikun/infinite-canvas/repository"
)

type GatewayLoginSource struct {
	Provider model.GatewayProvider `json:"provider"`
	BaseURL  string                `json:"baseUrl"`
	SiteHost string                `json:"siteHost"`
}

type GatewayLoginRequest struct {
	Provider model.GatewayProvider `json:"provider"`
	BaseURL  string                `json:"baseUrl"`
	Username string                `json:"username"`
	Password string                `json:"password"`
	SiteHost string                `json:"siteHost"`
}

type GatewayStatus struct {
	Connected bool                  `json:"connected"`
	Account   *GatewayAccountPublic `json:"account"`
	Models    []string              `json:"models"`
}

type GatewayAccountPublic struct {
	Provider    model.GatewayProvider `json:"provider"`
	BaseURL     string                `json:"baseUrl"`
	Username    string                `json:"username"`
	Email       string                `json:"email"`
	DisplayName string                `json:"displayName"`
	APIKeyReady bool                  `json:"apiKeyReady"`
	UpdatedAt   string                `json:"updatedAt"`
}

type GatewayLoginSession struct {
	model.AuthSession
	Account GatewayAccountPublic `json:"account"`
	Models  []string             `json:"models"`
	Notice  string               `json:"notice,omitempty"`
}

type gatewayLoginResult struct {
	Provider       model.GatewayProvider
	BaseURL        string
	ExternalUserID string
	Username       string
	Email          string
	DisplayName    string
	DistributorID  string
	DistributorSlug string
	SiteHost       string
	SessionCookie  string
}

type gatewayDistributorInfo struct {
	Belongs       bool
	DistributorID string
	Slug          string
	SiteHost      string
}

type gatewayKey struct {
	Key string
	ID  string
}

type gatewayFetchResult struct {
	Models []string
	Source string
}

type gatewayModelRow struct {
	ID       string
	Category string
}

var gatewayHTTPClient = &http.Client{Timeout: 30 * time.Second}

func LoginWithGateway(request GatewayLoginRequest) (GatewayLoginSession, error) {
	sources := normalizeGatewaySources([]GatewayLoginSource{{Provider: request.Provider, BaseURL: request.BaseURL, SiteHost: request.SiteHost}})
	if request.Provider == "" && strings.TrimSpace(request.BaseURL) != "" {
		sources = normalizeGatewaySources([]GatewayLoginSource{
			{Provider: model.GatewayProviderMain, BaseURL: request.BaseURL, SiteHost: request.SiteHost},
			{Provider: model.GatewayProviderSite, BaseURL: request.BaseURL, SiteHost: request.SiteHost},
		})
	}
	if len(sources) == 0 {
		return GatewayLoginSession{}, safeMessageError{message: "网关登录源未配置"}
	}
	var lastErr error
	for _, source := range sources {
		result, err := loginGatewaySource(source, request.Username, request.Password)
		if err != nil {
			lastErr = err
			continue
		}
		session, account, models, notice, err := prepareGatewayLogin(result)
		if err != nil {
			if retrySource, ok := gatewaySiteRetrySource(source, err); ok {
				retrySession, retryAccount, retryModels, retryNotice, retryErr := loginAndPrepareGatewaySource(retrySource, request.Username, request.Password)
				if retryErr == nil {
					return GatewayLoginSession{
						AuthSession: retrySession,
						Account:     publicGatewayAccount(retryAccount),
						Models:      retryModels,
						Notice:      retryNotice,
					}, nil
				}
				return GatewayLoginSession{}, retryErr
			}
			return GatewayLoginSession{}, err
		}
		return GatewayLoginSession{
			AuthSession: session,
			Account:     publicGatewayAccount(account),
			Models:      models,
			Notice:      notice,
		}, nil
	}
	if lastErr != nil {
		return GatewayLoginSession{}, lastErr
	}
	return GatewayLoginSession{}, safeMessageError{message: "网关登录失败"}
}

func LoginWithDefaultGateway(username string, password string) (GatewayLoginSession, bool, error) {
	if !config.Cfg.GatewayAllowLoginFallback {
		return GatewayLoginSession{}, false, nil
	}
	sources := DefaultGatewayLoginSources()
	if len(sources) == 0 {
		return GatewayLoginSession{}, false, nil
	}
	var lastErr error
	for _, source := range sources {
		result, err := loginGatewaySource(source, username, password)
		if err != nil {
			lastErr = err
			continue
		}
		session, account, models, notice, err := prepareGatewayLogin(result)
		if err != nil {
			if retrySource, ok := gatewaySiteRetrySource(source, err); ok {
				retrySession, retryAccount, retryModels, retryNotice, retryErr := loginAndPrepareGatewaySource(retrySource, username, password)
				if retryErr == nil {
					return GatewayLoginSession{
						AuthSession: retrySession,
						Account:     publicGatewayAccount(retryAccount),
						Models:      retryModels,
						Notice:      retryNotice,
					}, true, nil
				}
				return GatewayLoginSession{}, false, retryErr
			}
			return GatewayLoginSession{}, false, err
		}
		return GatewayLoginSession{
			AuthSession: session,
			Account:     publicGatewayAccount(account),
			Models:      models,
			Notice:      notice,
		}, true, nil
	}
	if lastErr != nil {
		return GatewayLoginSession{}, false, lastErr
	}
	return GatewayLoginSession{}, false, nil
}

func GatewayAccountStatus(userID string) (GatewayStatus, error) {
	account, ok, err := repository.FirstGatewayAccountByUser(userID)
	if err != nil || !ok {
		return GatewayStatus{Connected: false}, err
	}
	models := parseGatewayModels(account.Models)
	public := publicGatewayAccount(account)
	return GatewayStatus{Connected: true, Account: &public, Models: models}, nil
}

func RefreshGatewayModels(userID string) ([]string, error) {
	account, ok, err := repository.FirstGatewayAccountByUser(userID)
	if err != nil || !ok {
		return nil, safeMessageError{message: "未连接网关账号"}
	}
	fetched, err := fetchGatewayModels(account)
	if err != nil {
		return nil, err
	}
	account.Models = encodeGatewayModels(fetched.Models)
	account.ModelsSource = fetched.Source
	account.UpdatedAt = now()
	_, err = repository.SaveGatewayAccount(account)
	return fetched.Models, err
}

func UserGatewayChannel(userID string, modelName string) (model.ModelChannel, bool, error) {
	account, ok, err := repository.FirstGatewayAccountByUser(userID)
	if err != nil || !ok {
		return model.ModelChannel{}, false, err
	}
	if strings.TrimSpace(account.APIKey) == "" {
		return model.ModelChannel{}, false, nil
	}
	models := parseGatewayModels(account.Models)
	if len(models) > 0 && !containsString(models, modelName) {
		return model.ModelChannel{}, false, nil
	}
	return model.ModelChannel{
		Protocol: "openai",
		Name:     "Gateway",
		BaseURL:  runtimeGatewayBaseURL(account.BaseURL),
		APIKey:   account.APIKey,
		Models:   models,
		Weight:   1,
		Enabled:  true,
	}, true, nil
}

func PublicSettingsWithGateway(user model.AuthUser) (model.PublicSetting, error) {
	settings, err := PublicSettings()
	if err != nil {
		return settings, err
	}
	if user.ID == "" || user.Role == model.UserRoleGuest {
		return settings, nil
	}
	account, ok, err := repository.FirstGatewayAccountByUser(user.ID)
	if err != nil || !ok {
		return settings, err
	}
	models := parseGatewayModels(account.Models)
	if len(models) == 0 {
		return settings, nil
	}
	settings.ModelChannel.AvailableModels = models
	settings.ModelChannel.DefaultTextModel = repairDefaultModel(settings.ModelChannel.DefaultTextModel, models, isTextModelName)
	settings.ModelChannel.DefaultImageModel = repairDefaultModel(settings.ModelChannel.DefaultImageModel, models, isImageModelName)
	settings.ModelChannel.DefaultVideoModel = repairDefaultModel(settings.ModelChannel.DefaultVideoModel, models, isVideoModelName)
	settings.ModelChannel.DefaultModel = repairDefaultModel(settings.ModelChannel.DefaultModel, models, isTextModelName)
	enabled := false
	settings.ModelChannel.AllowCustomChannel = &enabled
	return settings, nil
}

func DefaultGatewayLoginSources() []GatewayLoginSource {
	raw := []GatewayLoginSource{}
	raw = append(raw, parseGatewaySources(config.Cfg.GatewayLoginSources)...)
	if config.Cfg.GatewayBaseURL != "" {
		raw = append(raw,
			GatewayLoginSource{Provider: model.GatewayProviderMain, BaseURL: config.Cfg.GatewayBaseURL, SiteHost: config.Cfg.GatewaySiteHost},
			GatewayLoginSource{Provider: model.GatewayProviderSite, BaseURL: config.Cfg.GatewayBaseURL, SiteHost: config.Cfg.GatewaySiteHost},
		)
	}
	return normalizeGatewaySources(raw)
}

func loginAndPrepareGatewaySource(source GatewayLoginSource, username string, password string) (model.AuthSession, model.GatewayAccount, []string, string, error) {
	result, err := loginGatewaySource(source, username, password)
	if err != nil {
		return model.AuthSession{}, model.GatewayAccount{}, nil, "", err
	}
	return prepareGatewayLogin(result)
}

func loginGatewaySource(source GatewayLoginSource, username string, password string) (gatewayLoginResult, error) {
	source.BaseURL = normalizeBaseURL(source.BaseURL)
	if source.Provider == "" || source.BaseURL == "" {
		return gatewayLoginResult{}, safeMessageError{message: "网关登录源未配置"}
	}
	if source.Provider == model.GatewayProviderSite {
		return loginSiteGateway(source.BaseURL, source.SiteHost, username, password)
	}
	return loginMainGateway(source.BaseURL, source.SiteHost, username, password)
}

func loginMainGateway(baseURL string, siteHost string, username string, password string) (gatewayLoginResult, error) {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	response, err := gatewayJSON(http.MethodPost, apiBaseURL(baseURL)+"/api/user/login", nil, body)
	if err != nil {
		return gatewayLoginResult{}, err
	}
	defer response.Body.Close()
	cookie := buildGatewayCookie(response.Header.Values("Set-Cookie"))
	if cookie == "" {
		return gatewayLoginResult{}, safeMessageError{message: "网关登录成功但未返回会话信息"}
	}
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return gatewayLoginResult{}, err
	}
	if isGatewayFalse(payload) {
		return gatewayLoginResult{}, safeMessageError{message: gatewayMessage(payload, "网关登录失败")}
	}
	user := extractGatewayUser(payload)
	externalID := anyString(user["id"])
	if externalID == "" {
		externalID = username
	}
	result := gatewayLoginResult{
		Provider:       model.GatewayProviderMain,
		BaseURL:        baseURL,
		ExternalUserID: externalID,
		Username:       firstNonEmpty(anyString(user["username"]), username),
		Email:          anyString(user["email"]),
		DisplayName:    firstNonEmpty(anyString(user["display_name"]), anyString(user["displayName"]), anyString(user["username"]), username),
		SessionCookie:  cookie,
	}
	if info, ok := fetchGatewayDistributorInfo(result); ok && info.Belongs {
		result.Provider = model.GatewayProviderSite
		result.DistributorID = info.DistributorID
		result.DistributorSlug = info.Slug
		result.SiteHost = resolveGatewaySiteHost(info, siteHost)
	}
	return result, nil
}

func loginSiteGateway(baseURL string, siteHost string, username string, password string) (gatewayLoginResult, error) {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	siteHost = resolveGatewaySiteHost(gatewayDistributorInfo{}, siteHost)
	response, err := gatewayJSON(http.MethodPost, apiBaseURL(baseURL)+"/api/dist/user/login", gatewaySiteHeaders(siteHost), body)
	if err != nil {
		return gatewayLoginResult{}, err
	}
	defer response.Body.Close()
	cookie := buildGatewayCookie(response.Header.Values("Set-Cookie"))
	if cookie == "" {
		return gatewayLoginResult{}, safeMessageError{message: "网关登录成功但未返回会话信息"}
	}
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return gatewayLoginResult{}, err
	}
	if isGatewayFalse(payload) {
		return gatewayLoginResult{}, safeMessageError{message: gatewayMessage(payload, "网关登录失败")}
	}
	user := extractGatewayUser(payload)
	externalID := anyString(user["id"])
	if externalID == "" {
		externalID = username
	}
	return gatewayLoginResult{
		Provider:       model.GatewayProviderSite,
		BaseURL:        baseURL,
		ExternalUserID: externalID,
		Username:       firstNonEmpty(anyString(user["username"]), username),
		Email:          anyString(user["email"]),
		DisplayName:    firstNonEmpty(anyString(user["display_name"]), anyString(user["displayName"]), anyString(user["username"]), username),
		SiteHost:       siteHost,
		SessionCookie:  cookie,
	}, nil
}

func fetchGatewayDistributorInfo(result gatewayLoginResult) (gatewayDistributorInfo, bool) {
	if result.SessionCookie == "" || result.ExternalUserID == "" {
		return gatewayDistributorInfo{}, false
	}
	response, err := gatewayJSON(http.MethodGet, apiBaseURL(result.BaseURL)+"/api/user/self/distributor", gatewayCookieHeadersFor(result.SessionCookie, result.ExternalUserID, result.SiteHost), nil)
	if err != nil {
		return gatewayDistributorInfo{}, false
	}
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return gatewayDistributorInfo{}, false
	}
	if isGatewayFalse(payload) {
		return gatewayDistributorInfo{}, false
	}
	data := extractGatewayData(payload)
	info := gatewayDistributorInfo{
		Belongs:       anyBool(data["belongs_to_distributor"]),
		DistributorID: anyString(data["distributor_id"]),
	}
	if distributor, ok := data["distributor"].(map[string]any); ok {
		info.Slug = anyString(distributor["slug"])
		info.SiteHost = firstNonEmpty(anyString(distributor["domain"]), anyString(distributor["host"]))
	}
	return info, info.Belongs
}

func gatewaySiteRetrySource(source GatewayLoginSource, err error) (GatewayLoginSource, bool) {
	if source.Provider == model.GatewayProviderSite || !isGatewaySiteKeyError(err) {
		return GatewayLoginSource{}, false
	}
	source.Provider = model.GatewayProviderSite
	source.SiteHost = resolveGatewaySiteHost(gatewayDistributorInfo{}, source.SiteHost)
	return source, true
}

func isGatewaySiteKeyError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "分站用户") || (strings.Contains(message, "分站") && strings.Contains(message, "密钥"))
}

func prepareGatewayLogin(result gatewayLoginResult) (model.AuthSession, model.GatewayAccount, []string, string, error) {
	account, ok, err := repository.GetGatewayAccountByExternal(result.Provider, result.BaseURL, result.ExternalUserID)
	if err != nil {
		return model.AuthSession{}, model.GatewayAccount{}, nil, "", err
	}
	nowText := now()
	if !ok {
		account = model.GatewayAccount{ID: newID("gateway"), CreatedAt: nowText}
	}
	account.Provider = result.Provider
	account.BaseURL = result.BaseURL
	account.ExternalUserID = result.ExternalUserID
	account.Username = result.Username
	account.Email = result.Email
	account.DisplayName = result.DisplayName
	account.DistributorID = result.DistributorID
	account.DistributorSlug = result.DistributorSlug
	account.SiteHost = resolveGatewaySiteHost(gatewayDistributorInfo{Slug: result.DistributorSlug, SiteHost: result.SiteHost}, result.SiteHost)
	account.SessionCookie = result.SessionCookie
	account.UpdatedAt = nowText

	key, err := ensureGatewayKey(account)
	if err != nil {
		return model.AuthSession{}, model.GatewayAccount{}, nil, "", err
	}
	account.APIKey = key.Key
	account.APIKeyID = key.ID

	fetched, err := fetchGatewayModels(account)
	if err != nil {
		return model.AuthSession{}, model.GatewayAccount{}, nil, "", err
	}
	user, err := upsertGatewayUser(result, ok, account.UserID)
	if err != nil {
		return model.AuthSession{}, model.GatewayAccount{}, nil, "", err
	}
	account.UserID = user.ID
	account.Models = encodeGatewayModels(fetched.Models)
	account.ModelsSource = fetched.Source
	account, err = repository.SaveGatewayAccount(account)
	if err != nil {
		return model.AuthSession{}, model.GatewayAccount{}, nil, "", err
	}
	session, err := newSession(user)
	return session, account, fetched.Models, gatewayNotice(fetched.Models), err
}

func upsertGatewayUser(result gatewayLoginResult, hasAccount bool, userID string) (model.User, error) {
	var user model.User
	var ok bool
	var err error
	if hasAccount && userID != "" {
		user, ok, err = repository.GetUserByID(userID)
		if err != nil {
			return user, err
		}
	}
	if !ok {
		username := gatewayLocalUsername(result)
		user = model.User{
			ID:          newID("user"),
			Username:    username,
			Email:       result.Email,
			DisplayName: result.DisplayName,
			Role:        model.UserRoleUser,
			AffCode:     newAffCode(),
			Status:      model.UserStatusActive,
			CreatedAt:   now(),
		}
	} else if user.Status == model.UserStatusBan {
		return user, safeMessageError{message: "账号已被禁用"}
	}
	user.Email = firstNonEmpty(result.Email, user.Email)
	user.DisplayName = firstNonEmpty(result.DisplayName, user.DisplayName)
	user.LastLoginAt = now()
	user.UpdatedAt = now()
	extra := map[string]any{}
	if user.Extra != "" {
		_ = json.Unmarshal([]byte(user.Extra), &extra)
	}
	extra["gateway"] = map[string]string{
		"provider":        string(result.Provider),
		"baseUrl":         result.BaseURL,
		"externalUserId":  result.ExternalUserID,
		"username":        result.Username,
		"distributorId":   result.DistributorID,
		"distributorSlug": result.DistributorSlug,
	}
	extraJSON, _ := json.Marshal(extra)
	user.Extra = string(extraJSON)
	return repository.SaveUser(user)
}

func gatewayLocalUsername(result gatewayLoginResult) string {
	base := strings.TrimSpace(result.Username)
	if base == "" {
		base = "gateway-" + result.ExternalUserID
	}
	base = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			return '-'
		}
		return r
	}, base)
	if _, ok, err := repository.GetUserByUsername(base); err != nil || !ok {
		return base
	}
	suffix := strings.TrimSpace(result.ExternalUserID)
	if suffix == "" {
		suffix = fmt.Sprint(time.Now().Unix())
	}
	candidate := base + "-" + suffix
	if _, ok, err := repository.GetUserByUsername(candidate); err != nil || !ok {
		return candidate
	}
	return candidate + "-" + fmt.Sprint(time.Now().Unix())
}

func ensureGatewayKey(account model.GatewayAccount) (gatewayKey, error) {
	if account.Provider == model.GatewayProviderSite {
		return ensureSiteGatewayKey(account)
	}
	return ensureMainGatewayKey(account)
}

func ensureMainGatewayKey(account model.GatewayAccount) (gatewayKey, error) {
	headers := gatewayCookieHeaders(account)
	tokens, err := listMainGatewayKeys(account.BaseURL, headers)
	if err != nil {
		return gatewayKey{}, err
	}
	if key := findGatewayAutoKey(tokens); key.Key != "" {
		return key, nil
	}
	name := autoGatewayKeyName()
	body := map[string]any{
		"name":                 name,
		"expired_time":         -1,
		"remain_quota":         0,
		"unlimited_quota":      true,
		"model_limits_enabled": false,
		"group":                defaultGatewayKeyGroup(),
	}
	if strings.TrimSpace(config.Cfg.GatewayKeyGroup) != "" {
		body["group"] = strings.TrimSpace(config.Cfg.GatewayKeyGroup)
	}
	payload, _ := json.Marshal(body)
	response, err := gatewayJSON(http.MethodPost, apiBaseURL(account.BaseURL)+"/api/token/", headers, payload)
	if err != nil {
		return gatewayKey{}, err
	}
	response.Body.Close()
	tokens, err = listMainGatewayKeys(account.BaseURL, headers)
	if err != nil {
		return gatewayKey{}, err
	}
	for _, item := range tokens {
		if anyString(item["name"]) == name {
			key := normalizeGatewayAPIKey(anyString(item["key"]))
			if key != "" {
				return gatewayKey{Key: key, ID: anyString(item["id"])}, nil
			}
		}
	}
	return gatewayKey{}, safeMessageError{message: "网关访问密钥已创建但未能读取"}
}

func ensureSiteGatewayKey(account model.GatewayAccount) (gatewayKey, error) {
	headers := gatewayCookieHeaders(account)
	tokens, err := listSiteGatewayKeys(account.BaseURL, headers)
	if err != nil {
		return gatewayKey{}, err
	}
	if key := findGatewayAutoKey(tokens); key.Key != "" {
		return key, nil
	}
	name := autoGatewayKeyName()
	body := map[string]any{"name": name}
	if config.Cfg.GatewayKeyGroupID > 0 {
		body["key_group_id"] = config.Cfg.GatewayKeyGroupID
	}
	payload, _ := json.Marshal(body)
	response, err := gatewayJSON(http.MethodPost, apiBaseURL(account.BaseURL)+"/api/dist/token/create", headers, payload)
	if err != nil {
		return gatewayKey{}, err
	}
	defer response.Body.Close()
	var data map[string]any
	_ = json.NewDecoder(response.Body).Decode(&data)
	if isGatewayFalse(data) {
		return gatewayKey{}, safeMessageError{message: gatewayMessage(data, "创建网关访问密钥失败")}
	}
	created := extractGatewayData(data)
	key := normalizeGatewayAPIKey(anyString(created["key"]))
	if key == "" {
		return gatewayKey{}, safeMessageError{message: "网关访问密钥已创建但响应中没有返回密钥"}
	}
	return gatewayKey{Key: key, ID: anyString(created["id"])}, nil
}

func listMainGatewayKeys(baseURL string, headers map[string]string) ([]map[string]any, error) {
	response, err := gatewayJSON(http.MethodGet, apiBaseURL(baseURL)+"/api/token/", headers, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var payload any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return extractGatewayItems(payload), nil
}

func listSiteGatewayKeys(baseURL string, headers map[string]string) ([]map[string]any, error) {
	response, err := gatewayJSON(http.MethodGet, apiBaseURL(baseURL)+"/api/dist/token/list?page=1&page_size=200", headers, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var payload any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return extractGatewayItems(payload), nil
}

func fetchGatewayModels(account model.GatewayAccount) (gatewayFetchResult, error) {
	if account.Provider == model.GatewayProviderMain {
		models, err := fetchMainSubscribedGatewayModels(account)
		if err != nil {
			return gatewayFetchResult{}, err
		}
		if len(models) > 0 {
			return gatewayFetchResult{Models: models, Source: "subscription"}, nil
		}
	}
	models, err := fetchGatewayModelList(account.BaseURL, account.APIKey)
	if err != nil {
		return gatewayFetchResult{}, err
	}
	return gatewayFetchResult{Models: models, Source: "gateway"}, nil
}

func fetchMainSubscribedGatewayModels(account model.GatewayAccount) ([]string, error) {
	response, err := gatewayJSON(http.MethodGet, apiBaseURL(account.BaseURL)+gatewaySubscribedModelsPath(), gatewayCookieHeaders(account), nil)
	if err != nil {
		if gatewayErr, ok := err.(safeMessageError); ok && strings.Contains(gatewayErr.message, "404") {
			return nil, nil
		}
		return nil, err
	}
	defer response.Body.Close()
	var payload any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, err
	}
	rows := extractGatewayItems(payload)
	models := make([]gatewayModelRow, 0, len(rows))
	for _, row := range rows {
		models = append(models, gatewayModelRow{ID: firstNonEmpty(anyString(row["model_name"]), anyString(row["modelName"]), anyString(row["id"]), anyString(row["name"])), Category: anyString(row["category"])})
	}
	return normalizeGatewayModels(models), nil
}

func fetchGatewayModelList(baseURL string, apiKey string) ([]string, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, nil
	}
	headers := map[string]string{"Authorization": "Bearer " + strings.TrimPrefix(apiKey, "Bearer ")}
	response, err := gatewayJSON(http.MethodGet, runtimeGatewayBaseURL(baseURL)+"/models", headers, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var payload any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, err
	}
	rows := extractGatewayItems(payload)
	models := make([]gatewayModelRow, 0, len(rows))
	for _, row := range rows {
		models = append(models, gatewayModelRow{ID: firstNonEmpty(anyString(row["id"]), anyString(row["model"]), anyString(row["name"])), Category: anyString(row["category"])})
	}
	return normalizeGatewayModels(models), nil
}

func gatewayJSON(method string, requestURL string, headers map[string]string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequest(method, requestURL, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		if strings.EqualFold(key, "Host") {
			request.Host = value
			continue
		}
		request.Header.Set(key, value)
	}
	response, err := gatewayHTTPClient.Do(request)
	if err != nil {
		return nil, safeMessageError{message: "网关连接失败"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		response.Body.Close()
		return nil, safeMessageError{message: gatewayHTTPError(response.StatusCode, body)}
	}
	return response, nil
}

func gatewayHTTPError(statusCode int, body []byte) string {
	if len(body) > 0 {
		var payload map[string]any
		if json.Unmarshal(body, &payload) == nil {
			if msg := gatewayMessage(payload, ""); msg != "" {
				return msg
			}
		}
	}
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return "网关鉴权失败"
	}
	return fmt.Sprintf("网关请求失败：%d", statusCode)
}

func normalizeGatewayModels(rows []gatewayModelRow) []string {
	set := map[string]bool{}
	for _, row := range rows {
		name := strings.TrimSpace(row.ID)
		if name == "" || isIgnoredGatewayModel(name) {
			continue
		}
		set[name] = true
	}
	result := make([]string, 0, len(set))
	for name := range set {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func isIgnoredGatewayModel(modelName string) bool {
	name := strings.ToLower(modelName)
	return strings.Contains(name, "embedding") || strings.Contains(name, "embed") || strings.Contains(name, "rerank") || strings.Contains(name, "moderation")
}

func parseGatewaySources(value string) []GatewayLoginSource {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var decoded []GatewayLoginSource
	if json.Unmarshal([]byte(value), &decoded) == nil {
		return decoded
	}
	result := []GatewayLoginSource{}
	for _, item := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		parts := strings.SplitN(strings.TrimSpace(item), "=", 2)
		if len(parts) == 2 {
			left := strings.TrimSpace(parts[0])
			provider := left
			siteHost := ""
			if providerParts := strings.SplitN(left, "@", 2); len(providerParts) == 2 {
				provider = strings.TrimSpace(providerParts[0])
				siteHost = strings.TrimSpace(providerParts[1])
			}
			result = append(result, GatewayLoginSource{Provider: model.GatewayProvider(provider), BaseURL: strings.TrimSpace(parts[1]), SiteHost: siteHost})
		} else if strings.HasPrefix(strings.ToLower(item), "http://") || strings.HasPrefix(strings.ToLower(item), "https://") {
			result = append(result,
				GatewayLoginSource{Provider: model.GatewayProviderMain, BaseURL: item},
				GatewayLoginSource{Provider: model.GatewayProviderSite, BaseURL: item},
			)
		}
	}
	return result
}

func normalizeGatewaySources(sources []GatewayLoginSource) []GatewayLoginSource {
	result := []GatewayLoginSource{}
	seen := map[string]bool{}
	for _, source := range sources {
		provider := normalizeGatewayProvider(source.Provider)
		baseURL := normalizeBaseURL(source.BaseURL)
		if provider == "" || baseURL == "" {
			continue
		}
		siteHost := normalizeGatewaySiteHost(source.SiteHost)
		if siteHost == "" {
			siteHost = normalizeGatewaySiteHost(config.Cfg.GatewaySiteHost)
		}
		key := string(provider) + ":" + baseURL + ":" + siteHost
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, GatewayLoginSource{Provider: provider, BaseURL: baseURL, SiteHost: siteHost})
	}
	return result
}

func normalizeGatewayProvider(provider model.GatewayProvider) model.GatewayProvider {
	value := strings.ToLower(strings.TrimSpace(string(provider)))
	switch value {
	case "main", "site":
		return model.GatewayProvider(value)
	default:
		return ""
	}
}

func normalizeBaseURL(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

func apiBaseURL(baseURL string) string {
	baseURL = normalizeBaseURL(baseURL)
	if strings.HasSuffix(strings.ToLower(baseURL), "/v1") {
		return strings.TrimRight(baseURL[:len(baseURL)-3], "/")
	}
	return baseURL
}

func runtimeGatewayBaseURL(accountBaseURL string) string {
	candidates := []string{
		config.Cfg.GatewayPublicBaseURL,
		config.Cfg.GatewayFallbackBaseURL,
	}
	candidates = append(candidates, splitGatewayCandidates(config.Cfg.GatewayBaseURLCandidates)...)
	candidates = append(candidates, accountBaseURL)
	for _, item := range candidates {
		baseURL := normalizeBaseURL(item)
		if baseURL != "" {
			return gatewayV1BaseURL(baseURL)
		}
	}
	return gatewayV1BaseURL(accountBaseURL)
}

func gatewayV1BaseURL(baseURL string) string {
	baseURL = normalizeBaseURL(baseURL)
	if strings.HasSuffix(strings.ToLower(baseURL), "/v1") {
		return baseURL
	}
	return baseURL + "/v1"
}

func splitGatewayCandidates(value string) []string {
	result := []string{}
	for _, item := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func defaultGatewayKeyGroup() string {
	return string([]byte{115, 117, 98, 114, 111, 117, 116, 101, 114})
}

func gatewaySubscribedModelsPath() string {
	path := strings.TrimSpace(config.Cfg.GatewaySubscribedModelsPath)
	if path != "" {
		if strings.HasPrefix(path, "/") {
			return path
		}
		return "/" + path
	}
	return "/api/user/self/" + defaultGatewayKeyGroup() + "/models"
}

func gatewayCookieHeaders(account model.GatewayAccount) map[string]string {
	return gatewayCookieHeadersFor(account.SessionCookie, account.ExternalUserID, account.SiteHost)
}

func gatewayCookieHeadersFor(cookie string, externalUserID string, siteHost string) map[string]string {
	headers := map[string]string{"Cookie": cookie}
	if externalUserID != "" {
		headers["New-Api-User"] = externalUserID
	}
	for key, value := range gatewaySiteHeaders(siteHost) {
		headers[key] = value
	}
	return headers
}

func gatewaySiteHeaders(siteHost string) map[string]string {
	siteHost = normalizeGatewaySiteHost(siteHost)
	if siteHost == "" {
		return nil
	}
	return map[string]string{
		"X-Original-Host":  siteHost,
		"X-Forwarded-Host": siteHost,
		"Host":             siteHost,
	}
}

func resolveGatewaySiteHost(info gatewayDistributorInfo, preferred string) string {
	if host := normalizeGatewaySiteHost(preferred); host != "" {
		return host
	}
	if host := normalizeGatewaySiteHost(info.SiteHost); host != "" {
		return host
	}
	if host := normalizeGatewaySiteHost(config.Cfg.GatewaySiteHost); host != "" {
		return host
	}
	slug := strings.TrimSpace(info.Slug)
	if slug == "" {
		return ""
	}
	if template := strings.TrimSpace(config.Cfg.GatewaySiteHostTemplate); template != "" {
		value := strings.ReplaceAll(template, "{slug}", slug)
		value = strings.ReplaceAll(value, "{id}", info.DistributorID)
		if host := normalizeGatewaySiteHost(value); host != "" {
			return host
		}
	}
	if suffix := normalizeGatewaySiteHost(config.Cfg.GatewaySiteHostSuffix); suffix != "" {
		return normalizeGatewaySiteHost(slug + "." + suffix)
	}
	if suffix := gatewayHostFromBaseURL(config.Cfg.GatewayPublicBaseURL); suffix != "" {
		return normalizeGatewaySiteHost(slug + "." + suffix)
	}
	return ""
}

func gatewayHostFromBaseURL(baseURL string) string {
	host := normalizeGatewaySiteHost(baseURL)
	if host == "" || isInternalGatewayHost(host) {
		return ""
	}
	return host
}

func normalizeGatewaySiteHost(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, ",") {
		value = strings.TrimSpace(strings.Split(value, ",")[0])
	}
	if strings.HasPrefix(value, "//") {
		value = "https:" + value
	}
	if strings.Contains(value, "://") {
		if parsed, err := url.Parse(value); err == nil {
			value = parsed.Host
		}
	}
	if index := strings.IndexAny(value, "/?#"); index >= 0 {
		value = value[:index]
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.Trim(strings.ToLower(value), ".")
	if value == "" || strings.ContainsAny(value, " \t\r\n@") {
		return ""
	}
	return value
}

func isInternalGatewayHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "localhost" ||
		host == "::1" ||
		strings.HasPrefix(host, "127.") ||
		strings.HasPrefix(host, "0.0.0.0") ||
		strings.HasSuffix(host, ".internal") ||
		strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".up.railway.app")
}

func buildGatewayCookie(values []string) string {
	parts := []string{}
	for _, value := range values {
		if cookie := strings.Split(value, ";")[0]; strings.TrimSpace(cookie) != "" {
			parts = append(parts, cookie)
		}
	}
	return strings.Join(parts, "; ")
}

func extractGatewayUser(payload map[string]any) map[string]any {
	if data, ok := payload["data"].(map[string]any); ok {
		if user, ok := data["user"].(map[string]any); ok {
			return user
		}
		return data
	}
	if user, ok := payload["user"].(map[string]any); ok {
		return user
	}
	return map[string]any{}
}

func extractGatewayData(payload map[string]any) map[string]any {
	if data, ok := payload["data"].(map[string]any); ok {
		return data
	}
	return payload
}

func extractGatewayItems(payload any) []map[string]any {
	for _, candidate := range gatewayCandidates(payload) {
		if items, ok := candidate.([]any); ok {
			return mapGatewayItems(items)
		}
	}
	return []map[string]any{}
}

func gatewayCandidates(payload any) []any {
	result := []any{payload}
	if root, ok := payload.(map[string]any); ok {
		result = append(result, root["items"], root["data"])
		if data, ok := root["data"].(map[string]any); ok {
			result = append(result, data["items"], data["data"])
		}
	}
	return result
}

func mapGatewayItems(items []any) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if mapped, ok := item.(map[string]any); ok {
			result = append(result, mapped)
		}
	}
	return result
}

func findGatewayAutoKey(items []map[string]any) gatewayKey {
	prefix := strings.TrimSpace(config.Cfg.GatewayKeyPrefix)
	for _, item := range items {
		name := anyString(item["name"])
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}
		key := normalizeGatewayAPIKey(anyString(item["key"]))
		if key != "" {
			return gatewayKey{Key: key, ID: anyString(item["id"])}
		}
	}
	return gatewayKey{}
}

func normalizeGatewayAPIKey(key string) string {
	key = strings.TrimSpace(strings.TrimPrefix(key, "Bearer "))
	if key == "" {
		return ""
	}
	if strings.HasPrefix(key, "sk-") {
		return key
	}
	return "sk-" + key
}

func autoGatewayKeyName() string {
	prefix := strings.TrimSpace(config.Cfg.GatewayKeyPrefix)
	if prefix == "" {
		prefix = "canvas-auto"
	}
	return fmt.Sprintf("%s-%d", prefix, time.Now().Unix())
}

func isGatewayFalse(payload map[string]any) bool {
	value, exists := payload["success"]
	return exists && value == false
}

func gatewayMessage(payload map[string]any, fallback string) string {
	for _, key := range []string{"message", "msg", "reason"} {
		if value := strings.TrimSpace(anyString(payload[key])); value != "" {
			return value
		}
	}
	if err, ok := payload["error"].(map[string]any); ok {
		if value := strings.TrimSpace(anyString(err["message"])); value != "" {
			return value
		}
	}
	return fallback
}

func anyString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		if typed == float64(int64(typed)) {
			return fmt.Sprint(int64(typed))
		}
		return fmt.Sprint(typed)
	case int:
		return fmt.Sprint(typed)
	case int64:
		return fmt.Sprint(typed)
	default:
		return ""
	}
}

func anyBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "y":
			return true
		default:
			return false
		}
	case float64:
		return typed != 0
	case int:
		return typed != 0
	case int64:
		return typed != 0
	default:
		return false
	}
}

func encodeGatewayModels(models []string) string {
	data, _ := json.Marshal(models)
	return string(data)
}

func parseGatewayModels(value string) []string {
	var models []string
	if value != "" {
		_ = json.Unmarshal([]byte(value), &models)
	}
	return uniqueModelNames(models)
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func publicGatewayAccount(account model.GatewayAccount) GatewayAccountPublic {
	return GatewayAccountPublic{
		Provider:    account.Provider,
		BaseURL:     account.BaseURL,
		Username:    account.Username,
		Email:       account.Email,
		DisplayName: account.DisplayName,
		APIKeyReady: strings.TrimSpace(account.APIKey) != "",
		UpdatedAt:   account.UpdatedAt,
	}
}

func gatewayNotice(models []string) string {
	if len(models) == 0 {
		return "未检测到可用模型，请确认账号权限或稍后刷新模型。"
	}
	return ""
}

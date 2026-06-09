package service

import (
	"encoding/json"
	"strings"

	"github.com/basketikun/infinite-canvas/model"
	"github.com/basketikun/infinite-canvas/repository"
)

const maxUserDataSnapshotBytes = 8 * 1024 * 1024

var allowedUserDataDomains = map[string]bool{
	"canvas":          true,
	"assets":          true,
	"image-workbench": true,
	"video-workbench": true,
}

type UserDataSnapshot struct {
	Domain    string          `json:"domain"`
	Data      json.RawMessage `json:"data"`
	UpdatedAt string          `json:"updatedAt"`
}

func GetUserDataSnapshot(user model.AuthUser, domain string) (UserDataSnapshot, error) {
	domain = normalizeUserDataDomain(domain)
	if !allowedUserDataDomains[domain] {
		return UserDataSnapshot{}, safeMessageError{message: "数据域不存在"}
	}
	item, ok, err := repository.GetUserDataSnapshot(user.ID, domain)
	if err != nil {
		return UserDataSnapshot{}, err
	}
	if !ok || strings.TrimSpace(item.Data) == "" {
		return UserDataSnapshot{Domain: domain, Data: json.RawMessage("null"), UpdatedAt: ""}, nil
	}
	return UserDataSnapshot{Domain: domain, Data: json.RawMessage(item.Data), UpdatedAt: item.UpdatedAt}, nil
}

func SaveUserDataSnapshot(user model.AuthUser, domain string, data json.RawMessage) (UserDataSnapshot, error) {
	domain = normalizeUserDataDomain(domain)
	if !allowedUserDataDomains[domain] {
		return UserDataSnapshot{}, safeMessageError{message: "数据域不存在"}
	}
	if len(data) == 0 {
		return UserDataSnapshot{}, safeMessageError{message: "数据不能为空"}
	}
	if len(data) > maxUserDataSnapshotBytes {
		return UserDataSnapshot{}, safeMessageError{message: "数据过大，请减少历史记录或媒体内容"}
	}
	if !json.Valid(data) {
		return UserDataSnapshot{}, safeMessageError{message: "数据格式错误"}
	}
	nowText := now()
	item, err := repository.SaveUserDataSnapshot(model.UserDataSnapshot{
		UserID:    user.ID,
		Domain:    domain,
		Data:      string(data),
		UpdatedAt: nowText,
	})
	if err != nil {
		return UserDataSnapshot{}, err
	}
	return UserDataSnapshot{Domain: item.Domain, Data: json.RawMessage(item.Data), UpdatedAt: item.UpdatedAt}, nil
}

func normalizeUserDataDomain(domain string) string {
	return strings.ToLower(strings.TrimSpace(domain))
}

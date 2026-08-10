package service

import (
	"encoding/json"
	"strings"

	"github.com/basketikun/infinite-canvas/model"
	"github.com/basketikun/infinite-canvas/repository"
)

const maxUserDataSnapshotBytes = 8 * 1024 * 1024

const canvasUserDataDomain = "canvas"

var allowedUserDataDomains = map[string]bool{
	canvasUserDataDomain: true,
	"assets":             true,
	"image-workbench":    true,
	"video-workbench":    true,
}

type UserDataSnapshot struct {
	Domain    string          `json:"domain"`
	Data      json.RawMessage `json:"data,omitempty"`
	UpdatedAt string          `json:"updatedAt"`
}

// CanvasProjectsPatch carries only the projects a client actually changed; KeepIDs is the
// client's full project id list in display order and doubles as the delete set.
type CanvasProjectsPatch struct {
	Projects []json.RawMessage `json:"projects"`
	KeepIDs  []string          `json:"keepIds"`
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
	// 回包不带 data：画布快照有几 MB，回显一遍等于把这次同步的流量翻倍。
	return UserDataSnapshot{Domain: item.Domain, UpdatedAt: item.UpdatedAt}, nil
}

// SaveUserCanvasProjects merges the changed projects into the stored canvas snapshot and
// prunes anything the client no longer lists, so a client uploads one project instead of
// its whole library on every edit.
func SaveUserCanvasProjects(user model.AuthUser, patch CanvasProjectsPatch) (UserDataSnapshot, error) {
	if len(patch.KeepIDs) == 0 {
		return UserDataSnapshot{}, safeMessageError{message: "缺少画布列表"}
	}
	stored, err := readCanvasProjects(user.ID)
	if err != nil {
		return UserDataSnapshot{}, err
	}
	ordered, err := mergeCanvasProjects(stored, patch)
	if err != nil {
		return UserDataSnapshot{}, err
	}
	data, err := json.Marshal(struct {
		Projects []json.RawMessage `json:"projects"`
	}{Projects: ordered})
	if err != nil {
		return UserDataSnapshot{}, err
	}
	return SaveUserDataSnapshot(user, canvasUserDataDomain, data)
}

// mergeCanvasProjects applies the patch onto the stored set and returns the projects in
// KeepIDs order; ids missing from KeepIDs are dropped, which is how deletes propagate.
func mergeCanvasProjects(stored map[string]json.RawMessage, patch CanvasProjectsPatch) ([]json.RawMessage, error) {
	for _, project := range patch.Projects {
		id, err := readCanvasProjectID(project)
		if err != nil {
			return nil, err
		}
		stored[id] = project
	}
	ordered := make([]json.RawMessage, 0, len(patch.KeepIDs))
	for _, id := range patch.KeepIDs {
		if project, ok := stored[id]; ok {
			ordered = append(ordered, project)
		}
	}
	return ordered, nil
}

func readCanvasProjects(userID string) (map[string]json.RawMessage, error) {
	item, ok, err := repository.GetUserDataSnapshot(userID, canvasUserDataDomain)
	if err != nil {
		return nil, err
	}
	projects := map[string]json.RawMessage{}
	if !ok || strings.TrimSpace(item.Data) == "" {
		return projects, nil
	}
	var payload struct {
		Projects []json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal([]byte(item.Data), &payload); err != nil {
		return nil, err
	}
	for _, project := range payload.Projects {
		// 存量里没有 id 的条目无法被 KeepIDs 引用，本来就会在重排时被丢掉，这里不必报错。
		id, err := readCanvasProjectID(project)
		if err != nil {
			continue
		}
		projects[id] = project
	}
	return projects, nil
}

func readCanvasProjectID(project json.RawMessage) (string, error) {
	var head struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(project, &head); err != nil {
		return "", safeMessageError{message: "画布数据格式错误"}
	}
	if strings.TrimSpace(head.ID) == "" {
		return "", safeMessageError{message: "画布数据缺少 id"}
	}
	return head.ID, nil
}

func normalizeUserDataDomain(domain string) string {
	return strings.ToLower(strings.TrimSpace(domain))
}

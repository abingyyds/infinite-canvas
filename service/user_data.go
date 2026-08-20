package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/basketikun/infinite-canvas/model"
	"github.com/basketikun/infinite-canvas/repository"
)

// 非画布的域仍然是一行一个 JSON 快照，整体读写。
const maxUserDataSnapshotBytes = 32 * 1024 * 1024

// 画布按项目分行存，所以限制的是单个画布，画布库总量不再设上限：
// 保存只写改动的那几行，库大了也不会让保存失败。
const maxCanvasProjectBytes = 8 * 1024 * 1024

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
	if domain == canvasUserDataDomain {
		return readCanvasSnapshot(user.ID)
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
	if !json.Valid(data) {
		return UserDataSnapshot{}, safeMessageError{message: "数据格式错误"}
	}
	// 老客户端仍会把整库发到这个通用接口，拆开走分行存储，不要再写回单行快照
	if domain == canvasUserDataDomain {
		patch, err := canvasPatchFromSnapshot(data)
		if err != nil {
			return UserDataSnapshot{}, err
		}
		return SaveUserCanvasProjects(user, patch)
	}
	if len(data) > maxUserDataSnapshotBytes {
		return UserDataSnapshot{}, oversizeSnapshotError(len(data))
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

// SaveUserCanvasProjects writes only the projects the client changed and drops anything KeepIDs
// no longer lists. Untouched projects are neither read nor rewritten, so the cost of a save
// tracks the edit instead of the size of the library.
func SaveUserCanvasProjects(user model.AuthUser, patch CanvasProjectsPatch) (UserDataSnapshot, error) {
	if len(patch.KeepIDs) == 0 {
		return UserDataSnapshot{}, safeMessageError{message: "缺少画布列表"}
	}
	if err := migrateLegacyCanvasSnapshot(user.ID); err != nil {
		return UserDataSnapshot{}, err
	}
	position := map[string]int{}
	for index, id := range patch.KeepIDs {
		position[id] = index
	}
	nowText := now()
	changed := make([]model.UserCanvasProject, 0, len(patch.Projects))
	for _, project := range patch.Projects {
		id, err := readCanvasProjectID(project)
		if err != nil {
			return UserDataSnapshot{}, err
		}
		if len(project) > maxCanvasProjectBytes {
			return UserDataSnapshot{}, oversizeCanvasProjectError(project)
		}
		index, kept := position[id]
		// 上传了却不在 keepIds 里的画布，客户端下一步就要删它，存了也是白存
		if !kept {
			continue
		}
		changed = append(changed, model.UserCanvasProject{
			UserID:    user.ID,
			ProjectID: id,
			SortIndex: index,
			Data:      string(project),
			UpdatedAt: nowText,
		})
	}
	if err := repository.SaveUserCanvasProjects(user.ID, changed, patch.KeepIDs); err != nil {
		return UserDataSnapshot{}, err
	}
	return UserDataSnapshot{Domain: canvasUserDataDomain, UpdatedAt: nowText}, nil
}

func readCanvasSnapshot(userID string) (UserDataSnapshot, error) {
	if err := migrateLegacyCanvasSnapshot(userID); err != nil {
		return UserDataSnapshot{}, err
	}
	items, err := repository.ListUserCanvasProjects(userID)
	if err != nil {
		return UserDataSnapshot{}, err
	}
	if len(items) == 0 {
		return UserDataSnapshot{Domain: canvasUserDataDomain, Data: json.RawMessage("null"), UpdatedAt: ""}, nil
	}
	updatedAt := ""
	body := bytes.Buffer{}
	body.WriteString(`{"projects":[`)
	for index, item := range items {
		if index > 0 {
			body.WriteString(",")
		}
		body.WriteString(item.Data)
		if item.UpdatedAt > updatedAt {
			updatedAt = item.UpdatedAt
		}
	}
	body.WriteString(`]}`)
	return UserDataSnapshot{Domain: canvasUserDataDomain, Data: json.RawMessage(body.Bytes()), UpdatedAt: updatedAt}, nil
}

// 老数据把整个画布库存成 user_data_snapshots 的一行，第一次读写时拆成行再删掉旧行。
// ponytail: 不加锁，并发请求最多重复拆一次，写进去的是同样的内容
func migrateLegacyCanvasSnapshot(userID string) error {
	item, ok, err := repository.GetUserDataSnapshot(userID, canvasUserDataDomain)
	if err != nil || !ok {
		return err
	}
	patch, err := canvasPatchFromSnapshot(json.RawMessage(item.Data))
	if err != nil {
		return err
	}
	return repository.MigrateUserCanvasSnapshot(userID, canvasUserDataDomain, legacyCanvasRows(userID, patch, item.UpdatedAt), patch.KeepIDs)
}

func legacyCanvasRows(userID string, patch CanvasProjectsPatch, updatedAt string) []model.UserCanvasProject {
	rows := make([]model.UserCanvasProject, 0, len(patch.KeepIDs))
	for index, project := range patch.Projects {
		rows = append(rows, model.UserCanvasProject{
			UserID:    userID,
			ProjectID: patch.KeepIDs[index],
			SortIndex: index,
			Data:      string(project),
			UpdatedAt: updatedAt,
		})
	}
	return rows
}

// canvasPatchFromSnapshot turns a whole-library {"projects":[...]} body into a patch that keeps
// exactly those projects, in that order. Entries without an id are dropped rather than rejected:
// KeepIDs could never reference them, so the blob format already lost them on every save.
func canvasPatchFromSnapshot(data json.RawMessage) (CanvasProjectsPatch, error) {
	if strings.TrimSpace(string(data)) == "" {
		return CanvasProjectsPatch{}, nil
	}
	var payload struct {
		Projects []json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return CanvasProjectsPatch{}, safeMessageError{message: "画布数据格式错误"}
	}
	patch := CanvasProjectsPatch{}
	for _, project := range payload.Projects {
		id, err := readCanvasProjectID(project)
		if err != nil {
			continue
		}
		patch.Projects = append(patch.Projects, project)
		patch.KeepIDs = append(patch.KeepIDs, id)
	}
	return patch, nil
}

func oversizeSnapshotError(size int) error {
	return safeMessageError{
		message: fmt.Sprintf("数据 %s 超出 %s 上限，请删除部分历史记录", formatSnapshotBytes(size), formatSnapshotBytes(maxUserDataSnapshotBytes)),
		status:  http.StatusRequestEntityTooLarge,
	}
}

// 只说"太大"用户不知道该动哪个画布，所以把画布名和实际大小一起说出来。
// 状态码给 413，否则失败以 200 回出去，HTTP 层的监控完全看不见。
func oversizeCanvasProjectError(project json.RawMessage) error {
	return safeMessageError{
		message: fmt.Sprintf("画布「%s」%s 超出单个画布 %s 上限，请删除其中部分节点",
			canvasProjectTitle(project), formatSnapshotBytes(len(project)), formatSnapshotBytes(maxCanvasProjectBytes)),
		status: http.StatusRequestEntityTooLarge,
	}
}

func canvasProjectTitle(project json.RawMessage) string {
	var head struct {
		Title string `json:"title"`
	}
	_ = json.Unmarshal(project, &head)
	title := strings.TrimSpace(head.Title)
	if title == "" {
		return "未命名"
	}
	return title
}

func formatSnapshotBytes(size int) string {
	return fmt.Sprintf("%.1fMB", float64(size)/(1024*1024))
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

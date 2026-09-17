package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	// 显式删除集。nil 表示老客户端没发，服务端退回按 KeepIDs 之外全删的老语义 —— 那个语义让
	// 任何一个还开着旧状态的标签页都能删掉别处新建的画布。
	DeleteIDs *[]string `json:"deleteIds"`
	// 客户端这次编辑所基于的版本号。缺某个 id 就跳过它的版本检查。
	BaseRevisions map[string]int64 `json:"baseRevisions"`
}

// CanvasSaveResult reports what the save actually did: the new revision of every row written,
// and the rows that were refused because someone else wrote them first.
type CanvasSaveResult struct {
	Domain    string           `json:"domain"`
	UpdatedAt string           `json:"updatedAt"`
	Revisions map[string]int64 `json:"revisions,omitempty"`
	Conflicts []CanvasConflict `json:"conflicts,omitempty"`
}

// CanvasConflict hands back the server copy so the client can merge against it instead of
// picking a whole-canvas winner and dropping the other side.
type CanvasConflict struct {
	ID       string          `json:"id"`
	Revision int64           `json:"revision"`
	Data     json.RawMessage `json:"data"`
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
		result, err := SaveUserCanvasProjects(user, patch)
		if err != nil {
			return UserDataSnapshot{}, err
		}
		return UserDataSnapshot{Domain: result.Domain, UpdatedAt: result.UpdatedAt}, nil
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
func SaveUserCanvasProjects(user model.AuthUser, patch CanvasProjectsPatch) (CanvasSaveResult, error) {
	if len(patch.KeepIDs) == 0 {
		return CanvasSaveResult{}, safeMessageError{message: "缺少画布列表"}
	}
	if err := migrateLegacyCanvasSnapshot(user.ID); err != nil {
		return CanvasSaveResult{}, err
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
			return CanvasSaveResult{}, err
		}
		if len(project) > maxCanvasProjectBytes {
			return CanvasSaveResult{}, oversizeCanvasProjectError(project)
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
	write := repository.CanvasWrite{Changed: changed, KeepIDs: patch.KeepIDs, BaseRevisions: patch.BaseRevisions}
	if patch.DeleteIDs != nil {
		write.DeleteIDs = *patch.DeleteIDs
		// 显式删除集可能是空的，用非 nil 空切片把它和"老客户端没发"区分开
		if write.DeleteIDs == nil {
			write.DeleteIDs = []string{}
		}
	}
	written, err := repository.SaveUserCanvasProjects(user.ID, write)
	if err != nil {
		return CanvasSaveResult{}, err
	}
	result := CanvasSaveResult{Domain: canvasUserDataDomain, UpdatedAt: nowText, Revisions: written.Applied}
	for _, row := range written.Conflicts {
		result.Conflicts = append(result.Conflicts, CanvasConflict{ID: row.ProjectID, Revision: row.Revision, Data: json.RawMessage(row.Data)})
	}
	return result, nil
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
		if isLaterTimestamp(item.UpdatedAt, updatedAt) {
			updatedAt = item.UpdatedAt
		}
	}
	// revisions 单独一张表而不是塞进画布 JSON：那个 JSON 客户端会原样存回来，混进去的字段会跟着回流。
	body.WriteString(`],"revisions":{`)
	for index, item := range items {
		if index > 0 {
			body.WriteString(",")
		}
		key, err := json.Marshal(item.ProjectID)
		if err != nil {
			return UserDataSnapshot{}, err
		}
		body.Write(key)
		body.WriteString(":")
		body.WriteString(strconv.FormatInt(item.Revision, 10))
	}
	body.WriteString(`}}`)
	return UserDataSnapshot{Domain: canvasUserDataDomain, Data: json.RawMessage(body.Bytes()), UpdatedAt: updatedAt}, nil
}

// 老数据把整个画布库存成 user_data_snapshots 的一行，第一次读写时拆成行再删掉旧行。
// 并发请求不加锁：谁在事务里删掉旧行谁才写行，见 repository.MigrateUserCanvasSnapshot。
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

// legacyCanvasRows expects a patch built by canvasPatchFromSnapshot: Projects[i] pairs with KeepIDs[i].
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

// RFC3339 带时区偏移时字典序不等于时间序，所以按解析后的时间比；解析不了的退回字符串比较。
func isLaterTimestamp(candidate string, current string) bool {
	if current == "" {
		return candidate != ""
	}
	a, errA := time.Parse(time.RFC3339, candidate)
	b, errB := time.Parse(time.RFC3339, current)
	if errA != nil || errB != nil {
		return candidate > current
	}
	return a.After(b)
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

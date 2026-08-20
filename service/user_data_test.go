package service

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basketikun/infinite-canvas/config"
	"github.com/basketikun/infinite-canvas/model"
	"github.com/basketikun/infinite-canvas/repository"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "infinite-canvas-user-data")
	if err != nil {
		panic(err)
	}
	config.Cfg.StorageDriver = "sqlite"
	config.Cfg.DatabaseDSN = filepath.Join(dir, "test.db")
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func seedCanvasProjects(t *testing.T, userID string, ids ...string) {
	t.Helper()
	rows := make([]model.UserCanvasProject, 0, len(ids))
	for index, id := range ids {
		rows = append(rows, model.UserCanvasProject{
			UserID:    userID,
			ProjectID: id,
			SortIndex: index,
			Data:      `{"id":"` + id + `","title":"` + id + `"}`,
			UpdatedAt: "2026-01-01T00:00:00Z",
		})
	}
	if err := repository.SaveUserCanvasProjects(userID, rows, ids); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func storedCanvas(t *testing.T, userID string) []model.UserCanvasProject {
	t.Helper()
	items, err := repository.ListUserCanvasProjects(userID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return items
}

func canvasIDs(items []model.UserCanvasProject) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ProjectID)
	}
	return ids
}

// 分行存储的全部意义：改一个画布不能把没动过的那些也重写一遍。
func TestSaveCanvasProjectsLeavesUntouchedRowsAlone(t *testing.T) {
	user := model.AuthUser{ID: "user-untouched"}
	seedCanvasProjects(t, user.ID, "a", "b", "c")

	_, err := SaveUserCanvasProjects(user, CanvasProjectsPatch{
		Projects: []json.RawMessage{json.RawMessage(`{"id":"b","title":"新的 B"}`)},
		KeepIDs:  []string{"a", "b", "c"},
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	items := storedCanvas(t, user.ID)
	if got := strings.Join(canvasIDs(items), ","); got != "a,b,c" {
		t.Fatalf("order = %s, want a,b,c", got)
	}
	for _, item := range items {
		if item.ProjectID == "b" {
			if item.Data != `{"id":"b","title":"新的 B"}` {
				t.Errorf("b data = %s, want the patched body", item.Data)
			}
			if item.UpdatedAt == "2026-01-01T00:00:00Z" {
				t.Error("b should have been rewritten")
			}
			continue
		}
		if item.UpdatedAt != "2026-01-01T00:00:00Z" {
			t.Errorf("%s was rewritten (updatedAt %s); only the patched project may be written", item.ProjectID, item.UpdatedAt)
		}
	}
}

func TestSaveCanvasProjectsDeletesWhatKeepIDsOmits(t *testing.T) {
	user := model.AuthUser{ID: "user-delete"}
	seedCanvasProjects(t, user.ID, "a", "b", "c")

	if _, err := SaveUserCanvasProjects(user, CanvasProjectsPatch{KeepIDs: []string{"a", "c"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := strings.Join(canvasIDs(storedCanvas(t, user.ID)), ","); got != "a,c" {
		t.Fatalf("remaining = %s, want a,c", got)
	}
}

// 只调顺序时客户端不会重传画布内容，顺序也必须跟着变。
func TestSaveCanvasProjectsReordersWithoutResendingData(t *testing.T) {
	user := model.AuthUser{ID: "user-reorder"}
	seedCanvasProjects(t, user.ID, "a", "b", "c")

	if _, err := SaveUserCanvasProjects(user, CanvasProjectsPatch{KeepIDs: []string{"c", "a", "b"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	items := storedCanvas(t, user.ID)
	if got := strings.Join(canvasIDs(items), ","); got != "c,a,b" {
		t.Fatalf("order = %s, want c,a,b", got)
	}
	if items[0].Data != `{"id":"c","title":"c"}` {
		t.Errorf("c data = %s, want it untouched by a reorder", items[0].Data)
	}
}

// 上传了但 keepIds 里没有的画布，客户端下一步就要删它，不该落库。
func TestSaveCanvasProjectsIgnoresProjectsOutsideKeepIDs(t *testing.T) {
	user := model.AuthUser{ID: "user-outside"}
	_, err := SaveUserCanvasProjects(user, CanvasProjectsPatch{
		Projects: []json.RawMessage{json.RawMessage(`{"id":"a"}`), json.RawMessage(`{"id":"ghost"}`)},
		KeepIDs:  []string{"a"},
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := strings.Join(canvasIDs(storedCanvas(t, user.ID)), ","); got != "a" {
		t.Fatalf("stored = %s, want a", got)
	}
}

func TestSaveCanvasProjectsRejectsProjectWithoutID(t *testing.T) {
	user := model.AuthUser{ID: "user-no-id"}
	_, err := SaveUserCanvasProjects(user, CanvasProjectsPatch{
		Projects: []json.RawMessage{json.RawMessage(`{"title":"no id"}`)},
		KeepIDs:  []string{"a"},
	})
	if err == nil || err.Error() != "画布数据缺少 id" {
		t.Fatalf("error = %v, want 画布数据缺少 id", err)
	}
}

// 老库是一整行 blob，第一次读的时候要原样拆成行，并且把旧行清掉。
func TestReadCanvasSnapshotMigratesLegacyBlob(t *testing.T) {
	user := model.AuthUser{ID: "user-legacy"}
	legacy := `{"projects":[{"id":"a","title":"A"},{"title":"没有 id"},{"id":"b","title":"B"}]}`
	if _, err := repository.SaveUserDataSnapshot(model.UserDataSnapshot{
		UserID: user.ID, Domain: "canvas", Data: legacy, UpdatedAt: "2026-01-02T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}

	snapshot, err := GetUserDataSnapshot(user, "canvas")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := `{"projects":[{"id":"a","title":"A"},{"id":"b","title":"B"}]}`
	if string(snapshot.Data) != want {
		t.Errorf("data = %s, want %s", snapshot.Data, want)
	}
	if snapshot.UpdatedAt != "2026-01-02T00:00:00Z" {
		t.Errorf("updatedAt = %q, want the legacy row's timestamp", snapshot.UpdatedAt)
	}
	if got := strings.Join(canvasIDs(storedCanvas(t, user.ID)), ","); got != "a,b" {
		t.Errorf("rows = %s, want a,b", got)
	}
	if _, ok, _ := repository.GetUserDataSnapshot(user.ID, "canvas"); ok {
		t.Error("the legacy blob row should be gone once it has been split")
	}
}

// 老客户端仍会往通用接口 POST 整库，也要落到分行存储上。
func TestSaveWholeCanvasSnapshotGoesThroughPerProjectRows(t *testing.T) {
	user := model.AuthUser{ID: "user-whole"}
	body := json.RawMessage(`{"projects":[{"id":"a","title":"A"},{"id":"b","title":"B"}]}`)
	if _, err := SaveUserDataSnapshot(user, "canvas", body); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := strings.Join(canvasIDs(storedCanvas(t, user.ID)), ","); got != "a,b" {
		t.Fatalf("rows = %s, want a,b", got)
	}
	if _, ok, _ := repository.GetUserDataSnapshot(user.ID, "canvas"); ok {
		t.Error("the canvas domain must not fall back to a single blob row")
	}
}

func TestReadCanvasSnapshotWithNoProjects(t *testing.T) {
	snapshot, err := GetUserDataSnapshot(model.AuthUser{ID: "user-empty"}, "canvas")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(snapshot.Data) != "null" {
		t.Errorf("data = %s, want null", snapshot.Data)
	}
}

func TestOversizeCanvasProjectErrorNamesTheCanvas(t *testing.T) {
	project := json.RawMessage(`{"id":"big","title":"电蚊拍主图","nodes":[` + strings.Repeat(`"x",`, 100) + `"x"]}`)
	err := oversizeCanvasProjectError(project)
	for _, want := range []string{"电蚊拍主图", "8.0MB"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, want it to contain %q", err.Error(), want)
		}
	}
	coded, ok := err.(interface{ SafeStatus() int })
	if !ok || coded.SafeStatus() != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %v, want %d so HTTP monitoring can see the failure", err, http.StatusRequestEntityTooLarge)
	}
}

func TestCanvasProjectTitleFallsBackWhenUntitled(t *testing.T) {
	if got := canvasProjectTitle(json.RawMessage(`{"id":"a","title":"  "}`)); got != "未命名" {
		t.Errorf("title = %q, want 未命名", got)
	}
}

// 两个请求同时读到 legacy blob 时只有赢得删除的事务能写行；
// 输家拿着陈旧 blob 重放时必须什么都不写，否则会覆盖赢家之后落库的编辑。
func TestMigrateLegacySnapshotLoserDoesNotReplayStaleBlob(t *testing.T) {
	user := model.AuthUser{ID: "user-migrate-race"}
	seedCanvasProjects(t, user.ID, "a")

	stale := []model.UserCanvasProject{{
		UserID: user.ID, ProjectID: "a", SortIndex: 0,
		Data: `{"id":"a","title":"stale"}`, UpdatedAt: "2020-01-01T00:00:00Z",
	}}
	if err := repository.MigrateUserCanvasSnapshot(user.ID, "canvas", stale, []string{"a"}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	items := storedCanvas(t, user.ID)
	if len(items) != 1 || items[0].Data != `{"id":"a","title":"a"}` {
		t.Fatalf("data = %+v, want the winner's row left untouched", items)
	}
}

// RFC3339 带时区偏移时字典序不等于时间序，UpdatedAt 要按时间挑最新的。
func TestReadCanvasSnapshotPicksChronologicallyLatestUpdatedAt(t *testing.T) {
	user := model.AuthUser{ID: "user-updated-at"}
	rows := []model.UserCanvasProject{
		// 16:00+08:00 是 08:00Z，字典序却排在 09:00Z 后面
		{UserID: user.ID, ProjectID: "a", SortIndex: 0, Data: `{"id":"a"}`, UpdatedAt: "2026-08-20T16:00:00+08:00"},
		{UserID: user.ID, ProjectID: "b", SortIndex: 1, Data: `{"id":"b"}`, UpdatedAt: "2026-08-20T09:00:00Z"},
	}
	if err := repository.SaveUserCanvasProjects(user.ID, rows, []string{"a", "b"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	snapshot, err := GetUserDataSnapshot(user, "canvas")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if snapshot.UpdatedAt != "2026-08-20T09:00:00Z" {
		t.Fatalf("updatedAt = %q, want 2026-08-20T09:00:00Z", snapshot.UpdatedAt)
	}
}

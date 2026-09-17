package repository

import (
	"errors"

	"github.com/basketikun/infinite-canvas/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ListUserCanvasProjects returns every project of a user in display order, data included.
func ListUserCanvasProjects(userID string) ([]model.UserCanvasProject, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	items := []model.UserCanvasProject{}
	err = db.Where("user_id = ?", userID).Order("sort_index asc").Find(&items).Error
	return items, err
}

// CanvasWrite carries one save: the rows to write, the display order, what to delete, and the
// revisions the client based its edit on.
type CanvasWrite struct {
	Changed []model.UserCanvasProject
	KeepIDs []string
	// nil 表示老客户端没有发删除集，仍按 KeepIDs 之外全删；非 nil 时只删列出来的。
	DeleteIDs []string
	// 缺某个 id 就跳过该画布的版本检查，老客户端因此保持原来的无条件覆盖。
	BaseRevisions map[string]int64
}

// CanvasWriteResult reports the new revision of every row that was written and the rows that
// were refused because the stored revision had moved on.
type CanvasWriteResult struct {
	Applied   map[string]int64
	Conflicts []model.UserCanvasProject
}

// SaveUserCanvasProjects writes the changed projects, repositions the ones that moved and drops
// the ones the client asked to delete, all in one transaction. Projects that did not change are
// never rewritten, which is the point of storing them per row. Rows whose stored revision no
// longer matches the client's base are left untouched and returned as conflicts.
func SaveUserCanvasProjects(userID string, write CanvasWrite) (CanvasWriteResult, error) {
	db, err := DB()
	if err != nil {
		return CanvasWriteResult{}, err
	}
	result := CanvasWriteResult{}
	err = db.Transaction(func(tx *gorm.DB) error {
		var txErr error
		result, txErr = writeUserCanvasProjects(tx, userID, write)
		return txErr
	})
	if err != nil {
		return CanvasWriteResult{}, err
	}
	return result, nil
}

// MigrateUserCanvasSnapshot splits a legacy whole-library row into per-project rows. Deleting the
// legacy row comes first and gates the write: two concurrent requests can both read the blob, and
// only the transaction that wins the delete may write rows — the loser would otherwise replay the
// stale blob over whatever the winner and any save after it just stored.
func MigrateUserCanvasSnapshot(userID string, domain string, rows []model.UserCanvasProject, keepIDs []string) error {
	db, err := DB()
	if err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		deleted := tx.Where("user_id = ? AND domain = ?", userID, domain).Delete(&model.UserDataSnapshot{})
		if deleted.Error != nil {
			return deleted.Error
		}
		if deleted.RowsAffected == 0 || len(keepIDs) == 0 {
			return nil
		}
		_, writeErr := writeUserCanvasProjects(tx, userID, CanvasWrite{Changed: rows, KeepIDs: keepIDs})
		return writeErr
	})
}

func writeUserCanvasProjects(tx *gorm.DB, userID string, write CanvasWrite) (CanvasWriteResult, error) {
	keepIDs := write.KeepIDs
	wanted := map[string]int{}
	for index, id := range keepIDs {
		wanted[id] = index
	}
	result := CanvasWriteResult{Applied: map[string]int64{}}
	legacy := make([]model.UserCanvasProject, 0, len(write.Changed))
	for _, row := range write.Changed {
		base, checked := write.BaseRevisions[row.ProjectID]
		if !checked {
			legacy = append(legacy, row)
			continue
		}
		applied, conflict, err := writeOneCanvasProject(tx, userID, row, base)
		if err != nil {
			return CanvasWriteResult{}, err
		}
		if conflict != nil {
			result.Conflicts = append(result.Conflicts, *conflict)
			continue
		}
		result.Applied[row.ProjectID] = applied
	}
	if len(legacy) > 0 {
		// 老客户端没报版本号，保持原来的无条件覆盖。revision 不在更新列里：覆盖它会把别处
		// 记下的版本重置掉，而老客户端本来就不参与冲突检测。
		conflict := clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "project_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"sort_index", "data", "updated_at"}),
		}
		// 分批：迁移时会一次写入用户的全部画布，单条 INSERT 的占位符个数有上限
		if err := tx.Clauses(conflict).CreateInBatches(&legacy, 100).Error; err != nil {
			return CanvasWriteResult{}, err
		}
	}
	if err := deleteUserCanvasProjects(tx, userID, write); err != nil {
		return CanvasWriteResult{}, err
	}
	stored := []model.UserCanvasProject{}
	if err := tx.Model(&model.UserCanvasProject{}).Select("project_id", "sort_index").Where("user_id = ?", userID).Find(&stored).Error; err != nil {
		return CanvasWriteResult{}, err
	}
	// 只改真正挪了位置的行，否则每次保存又变成把整库写一遍
	for _, item := range stored {
		target, ok := wanted[item.ProjectID]
		if !ok || target == item.SortIndex {
			continue
		}
		update := tx.Model(&model.UserCanvasProject{}).
			Where("user_id = ? AND project_id = ?", userID, item.ProjectID).
			Update("sort_index", target)
		if update.Error != nil {
			return CanvasWriteResult{}, update.Error
		}
	}
	return result, nil
}

// writeOneCanvasProject applies one row under its base revision. The update comes first so the
// common case is a single statement; only when it matches nothing do we look at whether the row
// is missing (a new project) or was written by someone else (a conflict).
func writeOneCanvasProject(tx *gorm.DB, userID string, row model.UserCanvasProject, base int64) (int64, *model.UserCanvasProject, error) {
	// COALESCE：AutoMigrate 给已有表加 revision 列时不会回填，历史行是 NULL 而不是 0，
	// 而 NULL = 0 在 SQL 里不成立。少了它，升级前就存在的画布会被永远判成冲突。
	update := tx.Model(&model.UserCanvasProject{}).
		Where("user_id = ? AND project_id = ? AND COALESCE(revision, 0) = ?", userID, row.ProjectID, base).
		Updates(map[string]any{
			"sort_index": row.SortIndex,
			"data":       row.Data,
			"updated_at": row.UpdatedAt,
			"revision":   gorm.Expr("COALESCE(revision, 0) + 1"),
		})
	if update.Error != nil {
		return 0, nil, update.Error
	}
	if update.RowsAffected > 0 {
		return base + 1, nil, nil
	}
	current := model.UserCanvasProject{}
	err := tx.Where("user_id = ? AND project_id = ?", userID, row.ProjectID).Take(&current).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		row.Revision = 1
		if err := tx.Create(&row).Error; err != nil {
			return 0, nil, err
		}
		return row.Revision, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	return 0, &current, nil
}

func deleteUserCanvasProjects(tx *gorm.DB, userID string, write CanvasWrite) error {
	if write.DeleteIDs == nil {
		// 老协议：keepIds 兼作删除集。旧标签页因此能删掉别处新建的画布，所以新客户端改发 DeleteIDs。
		return tx.Where("user_id = ? AND project_id NOT IN ?", userID, write.KeepIDs).Delete(&model.UserCanvasProject{}).Error
	}
	if len(write.DeleteIDs) == 0 {
		return nil
	}
	return tx.Where("user_id = ? AND project_id IN ?", userID, write.DeleteIDs).Delete(&model.UserCanvasProject{}).Error
}

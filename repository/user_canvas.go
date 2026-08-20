package repository

import (
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

// SaveUserCanvasProjects writes the changed projects, repositions the ones that moved and drops
// anything keepIDs no longer lists, all in one transaction. Projects that did not change are
// never rewritten, which is the point of storing them per row.
func SaveUserCanvasProjects(userID string, changed []model.UserCanvasProject, keepIDs []string) error {
	db, err := DB()
	if err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		return writeUserCanvasProjects(tx, userID, changed, keepIDs)
	})
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
		return writeUserCanvasProjects(tx, userID, rows, keepIDs)
	})
}

func writeUserCanvasProjects(tx *gorm.DB, userID string, changed []model.UserCanvasProject, keepIDs []string) error {
	wanted := map[string]int{}
	for index, id := range keepIDs {
		wanted[id] = index
	}
	if len(changed) > 0 {
		conflict := clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "project_id"}},
			UpdateAll: true,
		}
		// 分批：迁移时会一次写入用户的全部画布，单条 INSERT 的占位符个数有上限
		if err := tx.Clauses(conflict).CreateInBatches(&changed, 100).Error; err != nil {
			return err
		}
	}
	if err := tx.Where("user_id = ? AND project_id NOT IN ?", userID, keepIDs).Delete(&model.UserCanvasProject{}).Error; err != nil {
		return err
	}
	stored := []model.UserCanvasProject{}
	if err := tx.Model(&model.UserCanvasProject{}).Select("project_id", "sort_index").Where("user_id = ?", userID).Find(&stored).Error; err != nil {
		return err
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
			return update.Error
		}
	}
	return nil
}

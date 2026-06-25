package repository

import (
	"errors"

	"github.com/basketikun/infinite-canvas/model"
	"gorm.io/gorm"
)

func SaveImageTask(task model.ImageTask) (model.ImageTask, error) {
	db, err := DB()
	if err != nil {
		return task, err
	}
	return task, db.Save(&task).Error
}

func GetImageTaskByID(id string) (model.ImageTask, bool, error) {
	db, err := DB()
	if err != nil {
		return model.ImageTask{}, false, err
	}
	task := model.ImageTask{}
	err = db.Where("id = ?", id).First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.ImageTask{}, false, nil
	}
	return task, err == nil, err
}

func ListImageTasksByUser(userID string, limit int) ([]model.ImageTask, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	var items []model.ImageTask
	err = db.Where("user_id = ?", userID).Order("created_at desc").Limit(limit).Find(&items).Error
	return items, err
}

func ListImageTasksByStatus(statuses ...model.ImageTaskStatus) ([]model.ImageTask, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	if len(statuses) == 0 {
		return []model.ImageTask{}, nil
	}
	var items []model.ImageTask
	err = db.Where("status IN ?", statuses).Order("created_at asc").Find(&items).Error
	return items, err
}

func ListDueImageTaskIDs(limit int, dueBefore string) ([]string, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	var items []model.ImageTask
	err = db.Select("id").
		Where("status IN ?", []model.ImageTaskStatus{model.ImageTaskStatusPending, model.ImageTaskStatusRunning}).
		Where("(next_run_at = '' OR next_run_at IS NULL OR next_run_at <= ?)", dueBefore).
		Where("(locked_until = '' OR locked_until IS NULL OR locked_until <= ?)", dueBefore).
		Order("created_at asc").
		Limit(limit).
		Find(&items).Error
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids, nil
}

func ClaimImageTask(id string, workerID string, lockedUntil string, currentTime string) (bool, error) {
	db, err := DB()
	if err != nil {
		return false, err
	}
	result := db.Model(&model.ImageTask{}).
		Where("id = ?", id).
		Where("status IN ?", []model.ImageTaskStatus{model.ImageTaskStatusPending, model.ImageTaskStatusRunning}).
		Where("(next_run_at = '' OR next_run_at IS NULL OR next_run_at <= ?)", currentTime).
		Where("(locked_until = '' OR locked_until IS NULL OR locked_until <= ?)", currentTime).
		Updates(map[string]any{
			"status":       model.ImageTaskStatusRunning,
			"locked_by":    workerID,
			"locked_until": lockedUntil,
			"attempts":     gorm.Expr("COALESCE(attempts, 0) + 1"),
			"started_at":   gorm.Expr("CASE WHEN started_at = '' OR started_at IS NULL THEN ? ELSE started_at END", currentTime),
			"error_message": "",
			"next_run_at":   "",
			"updated_at":    currentTime,
		})
	return result.RowsAffected > 0, result.Error
}

func RenewImageTaskLease(id string, workerID string, lockedUntil string, currentTime string) (bool, error) {
	db, err := DB()
	if err != nil {
		return false, err
	}
	result := db.Model(&model.ImageTask{}).
		Where("id = ? AND locked_by = ? AND status = ?", id, workerID, model.ImageTaskStatusRunning).
		Updates(map[string]any{
			"locked_until": lockedUntil,
			"updated_at":   currentTime,
		})
	return result.RowsAffected > 0, result.Error
}

func UpdateImageTaskByOwner(id string, workerID string, values map[string]any) (bool, error) {
	db, err := DB()
	if err != nil {
		return false, err
	}
	result := db.Model(&model.ImageTask{}).Where("id = ? AND locked_by = ?", id, workerID).Updates(values)
	return result.RowsAffected > 0, result.Error
}

func MarkImageTaskRefunded(id string) error {
	db, err := DB()
	if err != nil {
		return err
	}
	return db.Model(&model.ImageTask{}).Where("id = ? AND refunded = ?", id, false).Update("refunded", true).Error
}

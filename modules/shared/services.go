package shared

import (
	"gorm.io/gorm"
)

func AddMember(DB *gorm.DB, BranchID, UserID uint) error {
	return DB.Table("branch_members").Create(map[string]interface{}{"user_id": UserID, "branch_id": BranchID}).Error
}

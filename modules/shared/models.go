package shared

import (
	"gorm.io/gorm"
)

type Business struct {
	gorm.Model
	OwnerID  *uint  `json:"ownerId"`
	Owner    *User  `json:"owner" gorm:"foreignKey:OwnerID"`
	Name     string `json:"name"`
	Location string `json:"location"`
}

type Branch struct {
	gorm.Model
	BusinessID *uint     `json:"businessId"`
	Business   *Business `json:"business" gorm:"foreignKey:BusinessID"`
	Name       string    `json:"name"`
	Members    []User    `json:"members" gorm:"many2many:branch_members"`
}

type User struct {
	gorm.Model
	Email    string   `json:"email"`
	Phone    string   `json:"phone"`
	Name     string   `json:"name"`
	Role     UserRole `json:"role" gorm:"default:default"`
	Photo    string   `json:"photo"`
	IsActive bool     `json:"isActive" gorm:"default:false"`
	Password string   `json:"password"`
	GoogleID string   `json:"googleId"`
	Branch   Branch   `json:"branch" gorm:"-"`
}

type UserRole string

const (
	Admin      UserRole = "admin"
	SuperAdmin UserRole = "super-admin"
	Default    UserRole = "default"
	Manager    UserRole = "manager"
)

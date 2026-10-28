package shared

import (
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type User struct {
	gorm.Model
	Email    string   `json:"email"`
	Phone    string   `json:"phone"`
	Name     string   `json:"name"`
	Role     UserRole `json:"role" gorm:"default:default"`
	Photo    string   `json:"photo"`
	IsActive bool     `json:"isActive" gorm:"default:false"`
	// Password holds the bcrypt hash. It is never serialised - User rows
	// are embedded in voucher/router responses, and leaking the hash
	// (or worse, a plaintext value written by an older code path) would
	// expose credentials to any client that can read those endpoints.
	Password string `json:"-"`
	GoogleID string `json:"googleId"`
}

type Currency string

const (
	UGX Currency = "UGX"
)

type Balance struct {
	Value    int      `json:"value"`
	Currency Currency `json:"currency"`
}

type Company struct {
	gorm.Model
	UserID  *uint                       `json:"userId"`
	User    User                        `json:"user" gorm:"foreignKey:UserID"`
	Members []User                      `json:"members" gorm:"many2many:company_members"`
	Balance datatypes.JSONType[Balance] `json:"balance"`
}

type UserRole string

const (
	Admin      UserRole = "admin"
	SuperAdmin UserRole = "super-admin"
	Default    UserRole = "default"
	Manager    UserRole = "manager"
)

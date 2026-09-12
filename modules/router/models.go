package router

import (
	"time"

	"github.com/kigongo-vincent/hotspot-be/modules/shared"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type VPNConnection string

const (
	Pending   VPNConnection = "pending"
	Connected VPNConnection = "connected"
	Default   VPNConnection = "default"
)

type Router struct {
	gorm.Model
	Name   string      `json:"name" gorm:"not null"`
	UserID *uint       `json:"userId"`
	User   shared.User `json:"user" gorm:"foreignKey:UserID"`
}

type CreateRouterRequest struct {
	Name string `json:"name"`
}

type CreateVPNRequest struct {
	RouterID *uint `json:"routerId"`
}

type KeyPair struct {
	Public  string `json:"public"`
	Private string `json:"private"`
}

type VPN struct {
	gorm.Model
	RemoteKeys     datatypes.JSONType[KeyPair] `json:"remoteKeys"`
	Address        string                      `json:"address"`
	RouterID       *uint                       `json:"routerId"`
	Router         Router                      `json:"router" gorm:"foreignKey:RouterID"`
	Status         VPNConnection               `json:"status" gorm:"default:default"`
	APIUsername    string                      `json:"-"`
	APIPasswordEnc string                      `json:"-" gorm:"column:api_password_enc"`
}

type RouterService struct {
	DB *gorm.DB
}

type RouterStat struct {
	gorm.Model
	RouterID uint    `gorm:"index;not null"`
	CPU      float64 `gorm:"not null"` // percent, 0-100
	Memory   float64 `gorm:"not null"` // percent used, 0-100
	RxBytes  uint64  `gorm:"not null"`
	TxBytes  uint64  `gorm:"not null"`

	ActiveUsers int `gorm:"not null"`
}

const RetentionWindow = 24 * time.Hour

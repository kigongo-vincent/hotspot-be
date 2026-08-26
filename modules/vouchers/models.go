package vouchers

import "gorm.io/gorm"

type VoucherService struct {
	DB *gorm.DB
}
